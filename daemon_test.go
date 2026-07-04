package main

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/crypto/nacl/box"
)

func linesFromReader(r io.Reader) chan string {
	lines := make(chan string)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		if scanner.Err() != nil {
			log.Println("Error reading from reader:", scanner.Err())
		}
	}()
	return lines
}

func startDaemon(port string) (io.WriteCloser, chan string, func()) {
	command := exec.Command("go", "run", ".", "--port", port, "--daemon")
	stdin, err := command.StdinPipe()
	if err != nil {
		panic(err)
	}

	stdout, err := command.StdoutPipe()
	if err != nil {
		panic(err)
	}

	lines := linesFromReader(stdout)

	err = command.Start()
	if err != nil {
		panic(err)
	}
	err = waitForPort(port, 5*time.Second)
	if err != nil {
		panic(err)
	}
	return stdin, lines, func() {
		stdin.Close()
		command.Process.Kill()
		command.Wait()
	}
}

func waitForPort(port string, timeout time.Duration) error {
	deadline := time.After(timeout)
	for {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 5*time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-deadline:
			return fmt.Errorf("port not ready within %s", timeout)
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func connectPeer(addr string) (*ConnState, func(), error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		log.Println("Peer dial failed:", err)
		if isDaemon {
			emitEvent("error", map[string]any{"msg": "dial failed: " + err.Error()})
		}
		return nil, nil, err
	}
	pubKey, privKey, err := box.GenerateKey(rand.Reader)
	if err != nil {
		log.Println("Error generating key:", err)
		if isDaemon {
			emitEvent("error", map[string]any{"msg": "key gen failed: " + err.Error()})
		}
		return nil, nil, err
	}
	identityPubKey, _, err := box.GenerateKey(rand.Reader)
	if err != nil {
		log.Println("Error generating identity key:", err)
		if isDaemon {
			emitEvent("error", map[string]any{"msg": "identity key gen failed: " + err.Error()})
		}
		return nil, nil, err
	}

	payload := append(pubKey[:], identityPubKey[:]...)
	if err := WriteFrame(ConnState{conn: conn}, payload, KeyExchange, 0); err != nil {
		log.Println("Error writing key exchange:", err)
		if isDaemon {
			emitEvent("error", map[string]any{"msg": "write key exchange failed: " + err.Error()})
		}
		return nil, nil, err
	}
	msgType, flags, body, err := ReadFrame(ConnState{conn: conn})
	if err != nil {
		log.Println("Error reading frame:", err)
		if isDaemon {
			emitEvent("error", map[string]any{"error": err.Error()})
		}
		return nil, nil, err
	}
	if msgType != KeyExchange {
		log.Println("Unexpected message type:", msgType)
		if isDaemon {
			emitEvent("error", map[string]any{"error": "unexpected message type(" + string(msgType) + ")"})
		}
		return nil, nil, err
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

	return &connState, func() {
		conn.Close()
		if isDaemon {
			peersMutex.Lock()
			delete(peers, connState.peerID)
			peersMutex.Unlock()
			emitEvent("peer_disconnected", map[string]any{
				"peer": connState.peerID,
			})
		}
	}, nil
}

func waitForEvent(event string, lines <-chan string) map[string]any {
	for line := range lines {
		var eventData map[string]any
		if err := json.Unmarshal([]byte(line), &eventData); err != nil {
			log.Println("Error unmarshalling event:", err)
			continue
		}
		if eventData["event"] == event {
			return eventData
		}
	}
	return nil
}

func waitForResponse(id int64, lines <-chan string) map[string]any {
	for line := range lines {
		var eventData map[string]any
		if err := json.Unmarshal([]byte(line), &eventData); err != nil {
			log.Println("Error unmarshalling event:", err)
			continue
		}
		if eventData["id"] != nil && eventData["id"].(float64) == float64(id) {
			return eventData
		}
	}
	return nil
}

func TestDaemonSendText(t *testing.T) {
	t.Log("=== Starting daemon ===")
	stdin, lines, cleanupDaemon := startDaemon("9077")
	defer cleanupDaemon()
	t.Log("Daemon started and port ready")

	t.Log("=== Connecting as peer ===")
	connState, cleanupConnect, err := connectPeer("127.0.0.1:9077")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupConnect()
	t.Log("Peer connected, key exchange complete")

	t.Log("=== Waiting for peer_connected IPC event ===")
	event := waitForEvent("peer_connected", lines)
	if event == nil {
		t.Fatal("peer_connected event not received")
	}
	peerID := event["id"].(string)
	t.Logf("Received peer_connected event — peer ID: %s", peerID)

	t.Log("=== Sending send_text IPC command ===")
	json.NewEncoder(stdin).Encode(map[string]any{
		"id":  1,
		"cmd": "send_text", "args": map[string]any{
			"target": peerID,
			"text":   "Hello, world!",
		},
	})
	t.Log("IPC command written to daemon stdin")

	t.Log("=== Waiting for IPC response ===")
	response := waitForResponse(1, lines)
	if response == nil || response["ok"] != true {
		t.Fatal("send_text failed")
	}
	t.Log("IPC response: ok=true")

	t.Log("=== Reading encrypted frame from TCP ===")
	msgType, _, body, err := ReadFrame(*connState)
	if err != nil {
		t.Fatalf("ReadFrame error: %v", err)
	}
	if msgType != Text {
		t.Fatalf("expected Text frame, got %d", msgType)
	}
	if string(body) != "Hello, world!" {
		t.Fatalf("expected 'Hello, world!', got '%s'", string(body))
	}
	t.Logf("Received Text frame via TCP: %q", string(body))
	t.Log("=== TEST PASSED ===")
}

func TestDaemonReceiveText(t *testing.T) {
	t.Log("=== Starting daemon ===")
	_, lines, cleanupDaemon := startDaemon("9078")
	defer cleanupDaemon()
	t.Log("Daemon started and port ready")

	t.Log("=== Connecting as peer ===")
	connState, cleanupConnect, err := connectPeer("127.0.0.1:9078")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupConnect()

	t.Log("=== Waiting for peer_connected IPC event ===")
	event := waitForEvent("peer_connected", lines)
	if event == nil {
		t.Fatal("peer_connected event not received")
	}
	peerID := event["id"].(string)
	t.Logf("Daemon assigned us peer ID: %s", peerID)

	t.Log("=== Sending Text frame over TCP ===")
	if err := WriteFrame(*connState, []byte("Hello back"), Text, Encrypted); err != nil {
		t.Fatal(err)
	}
	t.Log("Text frame sent via TCP")

	t.Log("=== Waiting for text_received IPC event ===")
	event2 := waitForEvent("text_received", lines)
	if event2 == nil {
		t.Fatal("text_received event not received")
	}
	from, _ := event2["from"].(string)
	text, _ := event2["text"].(string)
	t.Logf("text_received — from: %s, text: %s", from, text)
	if from != peerID {
		t.Fatalf("expected from=%s, got %s", peerID, from)
	}
	if text != "Hello back" {
		t.Fatalf("expected text='Hello back', got '%s'", text)
	}
	t.Log("=== TEST PASSED ===")
}

func TestDaemonReceiveFile(t *testing.T) {
	t.Log("=== Starting daemon ===")
	_, lines, cleanupDaemon := startDaemon("9079")
	defer cleanupDaemon()
	t.Log("Daemon started and port ready")

	t.Log("=== Connecting as peer ===")
	connState, cleanupConnect, err := connectPeer("127.0.0.1:9079")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupConnect()

	t.Log("=== Waiting for peer_connected IPC event ===")
	event := waitForEvent("peer_connected", lines)
	if event == nil {
		t.Fatal("peer_connected event not received")
	}
	t.Logf("Daemon assigned us peer ID: %s", event["id"].(string))

	content := []byte("test file content for StateTransfer")
	var rawID [16]byte
	rand.Read(rawID[:])
	transferID := hex.EncodeToString(rawID[:])

	meta := FileMetaData{
		ID:    transferID,
		Name:  "test_received.txt",
		Size:  int64(len(content)),
		Total: 1,
	}
	metaBody, _ := json.Marshal(meta)
	t.Logf("Sending FileMeta — ID: %s, Name: %s, Size: %d", transferID, meta.Name, meta.Size)

	if err := WriteFrame(*connState, metaBody, FileMeta, Encrypted); err != nil {
		t.Fatal(err)
	}

	t.Log("=== Waiting for file_transfer_started IPC event ===")
	event2 := waitForEvent("file_transfer_started", lines)
	if event2 == nil {
		t.Fatal("file_transfer_started event not received")
	}
	t.Logf("file_transfer_started received")

	chunk := make([]byte, 20+len(content))
	copy(chunk[:16], rawID[:])
	binary.LittleEndian.PutUint32(chunk[16:20], 0)
	copy(chunk[20:], content)

	t.Log("Sending FileChunk frame")
	if err := WriteFrame(*connState, chunk, FileChunk, Encrypted); err != nil {
		t.Fatal(err)
	}

	t.Log("=== Waiting for file_received IPC event ===")
	event3 := waitForEvent("file_received", lines)
	if event3 == nil {
		t.Fatal("file_received event not received")
	}
	t.Log("file_received event received")
	t.Log("=== TEST PASSED ===")
}
