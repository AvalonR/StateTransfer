package main

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type jsonWriter struct {
	encoder     *json.Encoder
	writerMutex sync.Mutex
}

/*
* Writes to stdout in JSON lines for IPC
 */
func (w *jsonWriter) write(v any) {
	w.writerMutex.Lock()
	defer w.writerMutex.Unlock()
	w.encoder.Encode(v)
}

var stdoutWriter = &jsonWriter{encoder: json.NewEncoder(os.Stdout)}

func emitResponse(id uint64, ok bool, data map[string]any) {
	m := map[string]any{"id": id, "ok": ok}
	maps.Copy(m, data)
	stdoutWriter.write(m)
}

func emitEvent(event string, data map[string]any) {
	m := map[string]any{"event": event}
	maps.Copy(m, data)
	stdoutWriter.write(m)
}

func daemonMode() {
	go stdinReader()
	waitForSignal()
}

type ipcEnvelope struct {
	ID   uint64          `json:"id"`
	Cmd  string          `json:"cmd"`
	Args json.RawMessage `json:"args"`
}

type sendTextArgs struct {
	Target string `json:"target"`
	Text   string `json:"text"`
}

type sendLinkArgs struct {
	Target string `json:"target"`
	Link   string `json:"link"`
}

type sendFileArgs struct {
	Target string `json:"target"`
	Path   string `json:"path,omitempty"`
	Name   string `json:"name,omitempty"`
	Data   string `json:"data,omitempty"`
}

type connectArgs struct {
	Addr string `json:"addr"`
}

func stdinReader() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var env ipcEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &env); err != nil {
			emitResponse(0, false, map[string]any{"error": "invalid json"})
			continue
		}
		switch env.Cmd {
		case "send_text":
			go handleSendText(env.ID, env.Args)
		case "send_link":
			go handleSentLink(env.ID, env.Args)
		case "send_file":
			go handleSendFile(env.ID, env.Args)
		case "connect":
			go connectToPeer(env.ID, env.Args)
		case "list_peers":
			go handleListPeers(env.ID)
		}
	}
	if err := scanner.Err(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// Handlers for stdin commands

func handleSendText(callID uint64, messageArgs json.RawMessage) {
	var args sendTextArgs
	if err := json.Unmarshal(messageArgs, &args); err != nil {
		emitResponse(callID, false, map[string]any{"error": "invalid json args"})
		return
	}
	peersMutex.Lock()
	peer, ok := peers[args.Target]
	peersMutex.Unlock()
	if !ok {
		emitResponse(callID, false, map[string]any{"error": "peer not found"})
		return
	}
	if err := WriteFrame(peer.conn, []byte(args.Text), Text, Encrypted); err != nil {
		emitResponse(callID, false, map[string]any{"error": err.Error()})
		return
	}
	emitResponse(callID, true, nil)
}

func handleSentLink(callID uint64, messageArgs json.RawMessage) {
	var args sendLinkArgs
	if err := json.Unmarshal(messageArgs, &args); err != nil {
		emitResponse(callID, false, map[string]any{"error": "invalid json args"})
		return
	}
	peersMutex.Lock()
	peer, ok := peers[args.Target]
	peersMutex.Unlock()
	if !ok {
		emitResponse(callID, false, map[string]any{"error": "peer not found"})
		return
	}
	if err := WriteFrame(peer.conn, []byte(args.Link), Link, Encrypted); err != nil {
		emitResponse(callID, false, map[string]any{"error": err.Error()})
		return
	}
	emitResponse(callID, true, nil)
}

type peerInfo struct {
	ID   string `json:"id"`
	Addr string `json:"addr"`
}

func handleListPeers(callID uint64) {
	peersMutex.Lock()
	defer peersMutex.Unlock()
	var out []peerInfo
	for id, p := range peers {
		out = append(out, peerInfo{ID: id, Addr: p.name})
	}
	emitResponse(callID, true, map[string]any{"peers": out})
}

func handleSendFile(callID uint64, messageArgs json.RawMessage) {
	var args sendFileArgs
	if err := json.Unmarshal(messageArgs, &args); err != nil {
		emitResponse(callID, false, map[string]any{"error": "invalid json args"})

		return
	}
	peersMutex.Lock()
	peer, ok := peers[args.Target]
	peersMutex.Unlock()
	if !ok {
		emitResponse(callID, false, map[string]any{"error": "peer not found"})
		return
	}
	sendFileStream(peer.conn, callID, args.Path)
	emitResponse(callID, true, nil)
}

func connectToPeer(callID uint64, messageArgs json.RawMessage) {
	var args connectArgs
	if err := json.Unmarshal(messageArgs, &args); err != nil {
		emitResponse(callID, false, map[string]any{"error": "invalid json args"})
		return
	}
	go dialKeyExchange(args.Addr)
	emitResponse(callID, true, nil)
}

func newTransferID() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

type FileMetaData struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Total   int       `json:"total"`
	ModTime time.Time `json:"mod_time"`
}

func sendFileStream(connState *ConnState, callID uint64, path string) {
	file, err := os.Open(path)
	if err != nil {
		emitResponse(callID, false, map[string]any{"error": err.Error()})
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		emitResponse(callID, false, map[string]any{"error": err.Error()})
		return
	}
	meta := FileMetaData{
		ID:      newTransferID(),
		Name:    filepath.Base(path),
		Size:    stat.Size(),
		Total:   (int(stat.Size()) + (maxWriteMessageSize - 20) - 1) / (maxWriteMessageSize - 20),
		ModTime: stat.ModTime(),
	}
	metaBody, err := json.Marshal(meta)
	if err != nil {
		emitResponse(callID, false, map[string]any{"error": err.Error()})
		return
	}
	if err := WriteFrame(connState, metaBody, FileMeta, Encrypted); err != nil {
		emitResponse(callID, false, map[string]any{"error": err.Error()})
		return
	}
	idRaw, err := hex.DecodeString(meta.ID)
	if err != nil {
		emitResponse(callID, false, map[string]any{"error": err.Error()})
		return
	}
	buf := make([]byte, maxWriteMessageSize)
	lastPct := -1
	for seq := 0; seq < meta.Total; seq++ {
		n, err := file.Read(buf[20:])
		if err != nil && err != io.EOF {
			emitResponse(callID, false, map[string]any{"error": err.Error()})
			return
		}
		copy(buf[:16], idRaw)
		binary.LittleEndian.PutUint32(buf[16:20], uint32(seq))

		payload := buf[:20+n]
		if err := WriteFrame(connState, payload, FileChunk, Encrypted); err != nil {
			emitResponse(callID, false, map[string]any{"error": err.Error()})
			return
		}
		pct := (seq + 1) * 100 / meta.Total
		if pct != lastPct {
			lastPct = pct
			emitEvent("file_progress", map[string]any{
				"id":    meta.ID,
				"pct":   pct,
				"total": meta.Total,
			})
		}
	}
}
