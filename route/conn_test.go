package route

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing/common/logger"
)

func TestConnectionCopyPreservesHalfCloseThroughTrackedConn(t *testing.T) {
	clientConn, inboundConn := tcpConnPair(t)
	outboundConn, targetConn := tcpConnPair(t)

	manager := &ConnectionManager{logger: logger.NOP()}
	trackedOutbound := manager.TrackConn(outboundConn)
	var done atomic.Bool
	copyDone := make(chan struct{}, 1)
	onClose := func(error) {
		select {
		case copyDone <- struct{}{}:
		default:
		}
	}
	go manager.connectionCopy(context.Background(), inboundConn, trackedOutbound, false, &done, onClose)
	go manager.connectionCopy(context.Background(), trackedOutbound, inboundConn, true, &done, onClose)

	request := []byte("half-close request")
	response := []byte("delayed response")
	_ = clientConn.SetDeadline(time.Now().Add(3 * time.Second))
	_ = targetConn.SetDeadline(time.Now().Add(3 * time.Second))

	targetDone := make(chan error, 1)
	go func() {
		got, err := io.ReadAll(targetConn)
		if err != nil {
			targetDone <- err
			return
		}
		if !bytes.Equal(got, request) {
			targetDone <- io.ErrUnexpectedEOF
			return
		}
		time.Sleep(20 * time.Millisecond)
		if _, err = targetConn.Write(response); err != nil {
			targetDone <- err
			return
		}
		targetDone <- targetConn.CloseWrite()
	}()

	if _, err := clientConn.Write(request); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if err := clientConn.CloseWrite(); err != nil {
		t.Fatalf("close request: %v", err)
	}
	got, err := io.ReadAll(clientConn)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !bytes.Equal(got, response) {
		t.Fatalf("response = %q, want %q", got, response)
	}
	if err := <-targetDone; err != nil {
		t.Fatalf("target exchange: %v", err)
	}
	select {
	case <-copyDone:
	case <-time.After(time.Second):
		t.Fatal("connection copy did not finish")
	}
}

func tcpConnPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen TCP pair: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	accepted := make(chan *net.TCPConn, 1)
	go func() {
		conn, acceptErr := listener.AcceptTCP()
		if acceptErr == nil {
			accepted <- conn
		}
	}()
	client, err := net.DialTCP("tcp", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatalf("dial TCP pair: %v", err)
	}
	var server *net.TCPConn
	select {
	case server = <-accepted:
	case <-time.After(time.Second):
		_ = client.Close()
		t.Fatal("accept TCP pair timed out")
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return client, server
}
