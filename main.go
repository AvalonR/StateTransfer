package main

import (
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/hashicorp/mdns"
	"golang.org/x/crypto/nacl/box"
)

func main() {
	excludeIP := flag.String("exclude-ip", "", "IP to exclude from local filter (for WSL testing)")
	port := flag.Int("port", 9092, "Port to listen on for local testing")
	peerAddr := flag.String("peer", "", "direct peer address to connect (ip:port)")
	flag.Parse()

	host, err := os.Hostname()
	if err != nil {
		panic(err)
	}
	instanceName := host + "-" + runtime.GOOS
	svc, err := mdns.NewMDNSService(instanceName, "_statetransfer._tcp", "", "", *port, nil, []string{"StateTransfer"})
	if err != nil {
		fmt.Println(err)
		panic(err)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		fmt.Println(err)
		panic(err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				fmt.Println(err)
				return
			}
			go acceptKeyExchange(conn)
		}
	}()

	server, err := mdns.NewServer(&mdns.Config{Zone: svc})
	if err != nil {
		fmt.Println(err)
		panic(err)
	}
	defer server.Shutdown()

	localIps := getLocalIps()
	if *excludeIP != "" {
		delete(localIps, *excludeIP)
	}
	fmt.Printf("Found local IPs: %v\n", localIps)

	entries := make(chan *mdns.ServiceEntry)
	go func() {
		for {
			entry := <-entries
			fmt.Printf("Found: %s at %s:%d\n", entry.Name, entry.AddrV4, entry.Port)
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

	waitForSignal()
}

func getLocalIps() map[string]bool {
	ips := make(map[string]bool)
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		fmt.Println(err)
		return ips
	}
	for _, addr := range addrs {
		fmt.Printf("  raw addr: %T %v\n", addr, addr)
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		default:
			fmt.Printf("  UNHANDLED TYPE: %T\n", addr)
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
	fmt.Printf("Dialing key exchange %s...\n", addr)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		fmt.Println("Peer dial failed:", err)
		return
	}
	defer conn.Close()
	pubKey, privKey, err := box.GenerateKey(rand.Reader)
	if err != nil {
		log.Println("Error generating key:", err)
		return
	}
	if err := WriteFrame(ConnState{conn: conn}, pubKey[:], KeyExchange, 0); err != nil {
		fmt.Println("Error writing key exchange:", err)
		return
	}
	msgType, flags, body, err := ReadFrame(ConnState{conn: conn})
	if err != nil {
		log.Println("Error reading frame:", err)
		return
	}
	if msgType != KeyExchange {
		log.Println("Unexpected message type:", msgType)
		return
	}
	if flags&Encrypted != 0 {
		log.Println("Received encrypted frame")
	}
	if flags&Fragmented != 0 {
		log.Println("Received fragmented frame")
	}
	sharedKey := new([32]byte)
	var theirPubKey [32]byte
	copy(theirPubKey[:], body)
	box.Precompute(sharedKey, &theirPubKey, privKey)
	connState := ConnState{conn: conn, sharedKey: sharedKey}
	log.Println("Key exchange complete")
	handleConnection(connState)
}

func acceptKeyExchange(conn net.Conn) {
	log.Printf("Accepting key exchange %s...", conn.RemoteAddr())
	pubKey, privKey, err := box.GenerateKey(rand.Reader)
	if err != nil {
		log.Println("Error generating key:", err)
		return
	}
	if err := WriteFrame(ConnState{conn: conn}, pubKey[:], KeyExchange, 0); err != nil {
		fmt.Println("Error writing key exchange:", err)
		return
	}
	msgType, flags, body, err := ReadFrame(ConnState{conn: conn})
	if err != nil {
		log.Println("Error reading frame:", err)
		return
	}
	if msgType != KeyExchange {
		log.Println("Unexpected message type:", msgType)
		return
	}
	if flags&Encrypted != 0 {
		log.Println("Received encrypted frame")
	}
	if flags&Fragmented != 0 {
		log.Println("Received fragmented frame")
	}
	sharedKey := new([32]byte)
	var theirPubKey [32]byte
	copy(theirPubKey[:], body)
	box.Precompute(sharedKey, &theirPubKey, privKey)
	connState := ConnState{conn: conn, sharedKey: sharedKey}
	log.Println("Key exchange complete")
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
	KeyExchange     MessageType = 0x07 // NaCl `box` public key (32 bytes).
)

type Flags byte

const (
	None       Flags = 0x00
	Encrypted  Flags = 0x01
	Fragmented Flags = 0x02
) // other bits are reserved

type ConnState struct {
	conn      net.Conn
	sharedKey *[32]byte // nullable shared key
}

// Current Frame [4B payload_length][2B version][1B type][1B flags]

// improtant writting a protocol

const maxWriteMessageSize = 16 * 1024 // 16 KB

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
	defer connState.conn.Close()

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
		case Link:
			log.Println("Received link:", string(body))
		case FileMeta:
			log.Println("Received file meta:", string(body))
		case FileChunk:
			log.Println("Received file chunk:", string(body))
		case VersionMismatch:
			log.Println("Received version mismatch:", string(body))
		}
		if flags&Fragmented != 0 {
			log.Println("Received fragmented frame")
		}
	}
}
