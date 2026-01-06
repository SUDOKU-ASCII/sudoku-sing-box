package sudoku

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestMuxSessionEcho(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	if err := WriteMuxPreface(clientConn); err != nil {
		t.Fatalf("WriteMuxPreface: %v", err)
	}
	if err := readMuxPreface(serverConn); err != nil {
		t.Fatalf("readMuxPreface: %v", err)
	}

	serverSession := newMuxSession(serverConn, func(stream *muxStream, payload []byte) {
		addr, err := DecodeAddress(bytes.NewReader(payload))
		if err != nil {
			stream.closeNoSend(err)
			return
		}
		if addr != "example.com:80" {
			stream.closeNoSend(io.ErrUnexpectedEOF)
			return
		}
		go io.Copy(stream, stream)
	})

	clientSession := newMuxSession(clientConn, nil)
	stream, err := dialMuxStream(clientSession, "example.com:80")
	if err != nil {
		t.Fatalf("dialMuxStream: %v", err)
	}

	const msg = "hello mux"
	if _, err := stream.Write([]byte(msg)); err != nil {
		t.Fatalf("stream.Write: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(stream, buf); err != nil {
		t.Fatalf("stream.Read: %v", err)
	}
	if string(buf) != msg {
		t.Fatalf("unexpected echo: got %q want %q", string(buf), msg)
	}

	_ = stream.Close()
	_ = clientConn.Close()

	select {
	case <-serverSession.closed:
	case <-time.After(2 * time.Second):
		t.Fatalf("server session did not close")
	}
}

