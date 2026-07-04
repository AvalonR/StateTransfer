package main

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/hashicorp/mdns"
	"golang.org/x/crypto/nacl/box"
)

var (
	identityPubKey  *[32]byte
	identityPrivKey *[32]byte
)

var (
	peersMutex sync.Mutex
	peers      = make(map[string]*PeerState) // keeyed by id (16 hex chars)
)

type activeTransfer struct {
	peerID  *[32]byte
	meta    *FileMetaData
	file    *os.File
	path    string
	written int64
}

var (
	transfersMu sync.Mutex
	transfers   = make(map[string]*activeTransfer) // keyed by transfer ID (hex, 32 chars)
)

var isDaemon bool

func main() {
	excludeIP := flag.String("exclude-ip", "", "IP to exclude from local filter (for WSL testing)")
	port := flag.Int("port", 9092, "Port to listen on for local testing")
	peerAddr := flag.String("peer", "", "direct peer address to connect (ip:port)")
	indentity := flag.Bool("identity", false, "Generate or load identity key")
	daemon := flag.Bool("daemon", false, "Generate or load identity key")
	flag.Parse()

	if *daemon {
		isDaemon = true
		log.SetOutput(io.Discard)
	}

	if *indentity {
		log.Println("Generating or loading identity key...")
		priv, pu := loadOrGenerateIdentity()
		if priv == nil || pu == nil {
			log.Println("Error loading or generating identity key")
			return
		}
		log.Println("Identity key loaded or generated:", hex.EncodeToString(priv[:]), hex.EncodeToString(pu[:]))
		return
	}
	identityPubKey, identityPrivKey = loadOrGenerateIdentity()
	if identityPubKey == nil || identityPrivKey == nil {
		log.Println("Error loading or generating identity key")
		return
	}

	host, err := os.Hostname()
	if err != nil {
		panic(err)
	}
	instanceName := host + "-" + runtime.GOOS
	svc, err := mdns.NewMDNSService(instanceName, "_statetransfer._tcp", "", "", *port, nil, []string{"StateTransfer"})
	if err != nil {
		log.Println(err)
		panic(err)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Println(err)
		panic(err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				log.Println(err)
				return
			}
			go acceptKeyExchange(conn)
		}
	}()

	server, err := mdns.NewServer(&mdns.Config{Zone: svc})
	if err != nil {
		log.Println(err)
		panic(err)
	}
	defer server.Shutdown()

	localIps := getLocalIps()
	if *excludeIP != "" {
		delete(localIps, *excludeIP)
	}
	log.Printf("Found local IPs: %v\n", localIps)

	entries := make(chan *mdns.ServiceEntry)
	go func() {
		for {
			entry := <-entries
			log.Printf("Found: %s at %s:%d\n", entry.Name, entry.AddrV4, entry.Port)
			if localIps[entry.AddrV4.String()] {
				continue
			}
			if entry.Name == instanceName+"._statetransfer._tcp.local." {
				continue
			}
			go dialKeyExchange(fmt.Sprintf("%s:%d", entry.AddrV4, entry.Port))
		}
	}()

	go func() {
		for {
			mdns.Lookup("_statetransfer._tcp", entries)
			time.Sleep(10 * time.Second)
		}
	}()
	if *peerAddr != "" {
		go dialKeyExchange(*peerAddr)
	}

	if isDaemon {
		daemonMode()
	} else {
		waitForSignal()
	}
}

func getLocalIps() map[string]bool {
	ips := make(map[string]bool)
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		log.Println(err)
		return ips
	}
	for _, addr := range addrs {
		log.Printf("  raw addr: %T %v\n", addr, addr)
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		default:
			log.Printf("  UNHANDLED TYPE: %T\n", addr)
		}
		if ip != nil && !ip.IsLoopback() {
			if ipv4 := ip.To4(); ipv4 != nil {
				ips[ipv4.String()] = true
			}
		}
	}
	return ips
}

func waitForSignal() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
}

func dialKeyExchange(addr string) {
	log.Printf("Dialing key exchange %s...\n", addr)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		log.Println("Peer dial failed:", err)
		if isDaemon {
			emitEvent("error", map[string]any{"msg": "dial failed: " + err.Error()})
		}
		return
	}
	defer conn.Close()
	pubKey, privKey, err := box.GenerateKey(rand.Reader)
	if err != nil {
		log.Println("Error generating key:", err)
		if isDaemon {
			emitEvent("error", map[string]any{"msg": "key gen failed: " + err.Error()})
		}
		return
	}
	payload := append(pubKey[:], identityPubKey[:]...)
	if err := WriteFrame(ConnState{conn: conn}, payload, KeyExchange, 0); err != nil {
		log.Println("Error writing key exchange:", err)
		if isDaemon {
			emitEvent("error", map[string]any{"msg": "write key exchange failed: " + err.Error()})
		}
		return
	}
	msgType, flags, body, err := ReadFrame(ConnState{conn: conn})
	if err != nil {
		log.Println("Error reading frame:", err)
		if isDaemon {
			emitEvent("error", map[string]any{"error": err.Error()})
		}
		return
	}
	if msgType != KeyExchange {
		log.Println("Unexpected message type:", msgType)
		if isDaemon {
			emitEvent("error", map[string]any{"error": "unexpected message type(" + string(msgType) + ")"})
		}
		return
	}
	if flags&Encrypted != 0 {
		log.Println("Received encrypted frame")
	}
	if flags&Fragmented != 0 {
		log.Println("Received fragmented frame")
	}
	sharedKey := new([32]byte)
	var theirEncPubKey [32]byte
	copy(theirEncPubKey[:], body)
	box.Precompute(sharedKey, &theirEncPubKey, privKey)
	var theirIdentityPubKey [32]byte
	copy(theirIdentityPubKey[:], body[32:])

	id := hex.EncodeToString(theirIdentityPubKey[:8])

	connState := ConnState{conn: conn, sharedKey: sharedKey, identityPub: &theirIdentityPubKey, peerID: id}

	peersMutex.Lock()
	peers[id] = &PeerState{id, connState.identityPub, conn.RemoteAddr().String(), &connState}
	peersMutex.Unlock()
	if isDaemon {
		emitEvent("peer_connected", map[string]any{
			"id":   id,
			"addr": conn.RemoteAddr().String(),
		})
	}
	log.Println("Key exchange complete, peer added to peer list: ", id)
	handleConnection(connState)
}

func acceptKeyExchange(conn net.Conn) {
	log.Printf("Accepting key exchange %s...", conn.RemoteAddr())
	pubKey, privKey, err := box.GenerateKey(rand.Reader)
	if err != nil {
		log.Println("Error generating key:", err)
		if isDaemon {
			emitEvent("error", map[string]any{"msg": "key gen failed: " + err.Error()})
		}
		return
	}
	payload := append(pubKey[:], identityPubKey[:]...)
	if err := WriteFrame(ConnState{conn: conn}, payload, KeyExchange, 0); err != nil {
		log.Println("Error writing key exchange:", err)
		if isDaemon {
			emitEvent("error", map[string]any{"msg": "write key exchange failed: " + err.Error()})
		}
		return
	}
	msgType, flags, body, err := ReadFrame(ConnState{conn: conn})
	if err != nil {
		log.Println("Error reading frame:", err)
		if isDaemon {
			emitEvent("error", map[string]any{"msg": "read frame failed: " + err.Error()})
		}
		return
	}
	if msgType != KeyExchange {
		log.Println("Unexpected message type:", msgType)
		if isDaemon {
			emitEvent("error", map[string]any{"msg": "unexpected msg type: " + string(msgType)})
		}
		return
	}
	if flags&Encrypted != 0 {
		log.Println("Received encrypted frame")
	}
	if flags&Fragmented != 0 {
		log.Println("Received fragmented frame")
	}
	sharedKey := new([32]byte)
	var theirEncPubKey [32]byte
	copy(theirEncPubKey[:], body)
	box.Precompute(sharedKey, &theirEncPubKey, privKey)
	var theirIdentityPubKey [32]byte
	copy(theirIdentityPubKey[:], body[32:])

	id := hex.EncodeToString(theirIdentityPubKey[:8])

	connState := ConnState{conn: conn, sharedKey: sharedKey, identityPub: &theirIdentityPubKey, peerID: id}

	peersMutex.Lock()
	peers[id] = &PeerState{id, connState.identityPub, conn.RemoteAddr().String(), &connState}
	peersMutex.Unlock()
	if isDaemon {
		emitEvent("peer_connected", map[string]any{
			"id":   id,
			"addr": conn.RemoteAddr().String(),
		})
	}
	log.Println("Key exchange complete, peer added to peer list: ", id)
	handleConnection(connState)
}

type MessageType byte

const (
	Ping            MessageType = 0x00 // Heartbeat / keepalive. Payload is empty.
	Pong            MessageType = 0x01 // Response to ping. Payload is empty.
	Text            MessageType = 0x02 // UTF-8 text payload.
	Link            MessageType = 0x03 // URL payload.
	FileMeta        MessageType = 0x04 // File metadata JSON.
	FileChunk       MessageType = 0x05 // Binary file chunk.
	VersionMismatch MessageType = 0x06 // Receiver sends this with expected version string in payload.
	KeyExchange     MessageType = 0x07 // NaCl `box` public key (32 bytes) + Sender's stable identity public key (32 bytes).
)

type Flags byte

const (
	None       Flags = 0x00
	Encrypted  Flags = 0x01
	Fragmented Flags = 0x02
) // other bits are reserved

type ConnState struct {
	conn        net.Conn
	sharedKey   *[32]byte // nullable shared key
	identityPub *[32]byte // peer's stable identity public key (not for encryption)
	peerID      string
}

type PeerState struct {
	id   string    // hex encoded first 8 bytes of identityPubKey
	pub  *[32]byte // full identityPubKey
	name string    // hostname
	conn *ConnState
}

// Current Frame [4B payload_length][2B version][1B type][1B flags]

// improtant writting a protocol

const maxWriteMessageSize = 256 * 1024 // 256 KB

func WriteFrame(connState ConnState, data []byte, msgType MessageType, flags Flags) error {
	if len(data) > maxWriteMessageSize {
		return fmt.Errorf("message too large: %d > %d", len(data), maxWriteMessageSize)
	}

	header := make([]byte, 8)
	if connState.sharedKey != nil {
		// 24 bytes for nonce, 16 bytes for auth tag
		binary.LittleEndian.PutUint32(header[0:4], uint32(len(data)+16))
	} else {
		binary.LittleEndian.PutUint32(header[0:4], uint32(len(data)))
	}
	// version (0x0100 = v1.0)
	binary.LittleEndian.PutUint16(header[4:6], 0x0100)
	header[6] = byte(msgType)
	if connState.sharedKey != nil {
		header[7] = byte(flags | Encrypted)
	} else {
		header[7] = byte(flags)
	}
	if _, err := connState.conn.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if connState.sharedKey != nil {
		nonce := make([]byte, 24)
		if _, err := rand.Read(nonce); err != nil {
			return fmt.Errorf("generate nonce: %w", err)
		}
		if _, err := connState.conn.Write(nonce); err != nil {
			return fmt.Errorf("write nonce: %w", err)
		}
		data = box.SealAfterPrecomputation(nil, data, (*[24]byte)(nonce), connState.sharedKey)
	}
	if _, err := connState.conn.Write(data); err != nil {
		return fmt.Errorf("write body: %w", err)
	}

	log.Println("Wrote frame (MessageType, Flags, Length(Data)): ", msgType, flags, len(data))
	return nil
}

func ReadFrame(connState ConnState) (MessageType, Flags, []byte, error) {
	header := make([]byte, 8)
	if _, err := io.ReadFull(connState.conn, header); err != nil {
		return 0, 0, nil, fmt.Errorf("read header: %w", err)
	}
	length := binary.LittleEndian.Uint32(header[0:4])
	if length > maxWriteMessageSize*4 {
		return 0, 0, nil, fmt.Errorf("oversized frame: length %d > %d", length, maxWriteMessageSize*4)
	}
	version := binary.LittleEndian.Uint16(header[4:6])
	msgType := MessageType(header[6])
	flags := Flags(header[7])
	if version != 0x0100 {
		WriteFrame(connState, []byte(fmt.Sprintf("Version mismatch: expected 0x0100, got 0x%04x", version)), VersionMismatch, 0)
		return 0, 0, nil, fmt.Errorf("version mismatch: expected 0x0100, got 0x%x", version)
	}

	if flags&Encrypted != 0 && length < 16 {
		return 0, 0, nil, fmt.Errorf("underflow: encrypted frame payload %d < 16 bytes", length)
	}
	body := make([]byte, length)
	if connState.sharedKey != nil && flags&Encrypted != 0 {
		nonce := make([]byte, 24)
		if _, err := io.ReadFull(connState.conn, nonce); err != nil {
			return 0, 0, nil, fmt.Errorf("read nonce: %w", err)
		}
		if _, err := io.ReadFull(connState.conn, body); err != nil {
			return 0, 0, nil, fmt.Errorf("read body: %w", err)
		}
		var ok bool
		body, ok = box.OpenAfterPrecomputation(nil, body, (*[24]byte)(nonce), connState.sharedKey)
		if !ok {
			return 0, 0, nil, fmt.Errorf("decryption failed")
		}

	} else {
		if _, err := io.ReadFull(connState.conn, body); err != nil {
			return 0, 0, nil, fmt.Errorf("read body: %w", err)
		}
	}

	return msgType, flags, body, nil
}

func handleConnection(connState ConnState) {
	defer func() {
		connState.conn.Close()
		if isDaemon {
			peersMutex.Lock()
			delete(peers, connState.peerID)
			peersMutex.Unlock()
			emitEvent("peer_disconnected", map[string]any{
				"peer": connState.peerID,
			})
		}
		transfersMu.Lock()
		for _, transfer := range transfers {
			if *transfer.peerID == *connState.identityPub {
				if transfer.file != nil {
					transfer.file.Close()
					os.Remove(transfer.path)
				}
				delete(transfers, transfer.meta.ID)
			}
		}
	}()

	for {
		msgType, flags, body, err := ReadFrame(connState)
		if err != nil {
			log.Printf("Error reading frame: %v", err)
			return
		}
		if flags&Encrypted != 0 {
			log.Println("Received encrypted frame")
		}
		switch msgType {
		case Ping:
			log.Println("Received ping")
			if err := WriteFrame(connState, nil, Pong, Encrypted); err != nil {
				log.Println("Error writing pong:", err)
				return
			}
		case Pong:
			log.Println("Received pong")
		case Text:
			log.Println("Received text:", string(body))
			if isDaemon {
				emitEvent("text_received", map[string]any{
					"from": connState.peerID,
					"text": string(body),
				})
			}
		case Link:
			log.Println("Received link:", string(body))
			if isDaemon {
				emitEvent("link_received", map[string]any{
					"from": connState.peerID,
					"link": string(body),
				})
			}
		case FileMeta:
			log.Println("Received file meta:", string(body))
			var fileMetaData FileMetaData
			err = json.Unmarshal(body, &fileMetaData)
			if err != nil {
				log.Println("Error unmarshaling file meta:", err)
				return
			}
			file, err := os.CreateTemp("", "st-*")
			if err != nil {
				log.Println("Error creating temp file:", err)
				return
			}
			transfersMu.Lock()
			transfers[fileMetaData.ID] = &activeTransfer{
				peerID:  connState.identityPub,
				meta:    &fileMetaData,
				file:    file,
				path:    filepath.Join(os.TempDir(), fileMetaData.Name),
				written: 0,
			}
			transfersMu.Unlock()

			if isDaemon {
				emitEvent("file_transfer_started", map[string]any{
					"from": connState.peerID,
					"meta": fileMetaData,
				})
			}

		case FileChunk:
			log.Println("Received file chunk:", string(body))
			if len(body) < 20 {
				log.Println("Error: file chunk too small")
				return
			}
			id := hex.EncodeToString(body[:16])
			seq := binary.LittleEndian.Uint32(body[16:20])
			transfersMu.Lock()
			transfer, ok := transfers[id]
			transfersMu.Unlock()
			if !ok {
				log.Println("Error: unknown transfer id:", id)
				return
			}
			offset := int64(seq) * (maxWriteMessageSize - 20)
			if _, err := transfer.file.WriteAt(body[20:], offset); err != nil {
				log.Println("Error writing file:", err)
				return
			}
			transfer.written += int64(len(body[20:]))
			if transfer.written >= transfer.meta.Size {
				transfer.file.Close()
				os.Rename(transfer.path, filepath.Join(os.TempDir(), transfer.meta.Name))
				transfersMu.Lock()
				delete(transfers, id)
				transfersMu.Unlock()
				if isDaemon {
					emitEvent("file_received", map[string]any{
						"from": connState.peerID,
						"meta": transfer.meta,
					})
				}
			}

		case VersionMismatch:
			log.Println("Received version mismatch:", string(body))
		}
		if flags&Fragmented != 0 {
			log.Println("Received fragmented frame")
		}
	}
}

func loadOrGenerateIdentity() (priv *[32]byte, pub *[32]byte) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		log.Println("Error getting user config dir:", err)
		return
	}
	dir := filepath.Join(configDir, "statetransfer")
	path := filepath.Join(dir, "identity")
	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) != 64 {
			log.Println("Error loading identity.key: invalid length")
			return
		}
		var p, q [32]byte
		copy(p[:], data[:32])
		copy(q[:], data[32:])
		return &p, &q
	} else if os.IsNotExist(err) {
		// Generate a new key pair if none exists
		priv, pub, err = box.GenerateKey(rand.Reader)
		if err != nil {
			log.Println("Error generating pub key:", err)
			return
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			log.Println("Error creating config dir:", err)
			return
		}
		if err := os.WriteFile(path, append(priv[:], pub[:]...), 0o600); err != nil {
			log.Println("Error writing identity.key:", err)
			return
		}
		return priv, pub
	} else {
		log.Println("Error reading identity:", err)
		return
	}
}
