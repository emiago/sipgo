package sip

import (
	"bytes"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/gobwas/ws"
)

// ws.WriteFrame writes a frame's header and payload in two calls, so
// concurrent writes on one connection must not split a frame.
func TestWSConnectionConcurrentWritesKeepFramesWhole(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- conn
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, ok := <-accepted
	if !ok {
		t.Fatal("accept failed")
	}
	conn := &WSConnection{Conn: server}
	defer conn.Close()

	const writers, perWriter = 16, 200
	payloads := make(map[string]bool, writers)
	for i := range writers {
		p := bytes.Repeat([]byte{byte('a' + i)}, 1500)
		payloads[string(p)] = true
	}

	var wg sync.WaitGroup
	for p := range payloads {
		wg.Add(1)
		go func(p []byte) {
			defer wg.Done()
			for range perWriter {
				if _, err := conn.Write(p); err != nil {
					return
				}
			}
		}([]byte(p))
	}

	if err := client.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for i := range writers * perWriter {
		f, err := ws.ReadFrame(client)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if f.Header.OpCode != ws.OpText || !f.Header.Fin || !payloads[string(f.Payload)] {
			t.Fatalf("frame %d is corrupt: %s", i, describeFrame(f))
		}
	}
	wg.Wait()
}

func describeFrame(f ws.Frame) string {
	head := f.Payload
	if len(head) > 16 {
		head = head[:16]
	}
	return fmt.Sprintf("op=%v fin=%v len=%d head=%q", f.Header.OpCode, f.Header.Fin, len(f.Payload), head)
}
