package sudoku

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
)

// MuxClient opens multiple target connections over a single already-upgraded Sudoku tunnel.
//
// It is intended to reduce per-connection RTT when HTTPMask tunnel modes are enabled by keeping one long-lived
// tunnel and opening lightweight sub-streams for each destination.
type MuxClient struct {
	dialBase func(ctx context.Context) (net.Conn, error)

	mu       sync.Mutex
	cond     *sync.Cond
	creating bool
	session  *muxSession
}

func NewMuxClient(dialBase func(ctx context.Context) (net.Conn, error)) *MuxClient {
	return &MuxClient{dialBase: dialBase}
}

func (c *MuxClient) Dial(ctx context.Context, targetAddr string) (net.Conn, error) {
	if c == nil || c.dialBase == nil {
		return nil, fmt.Errorf("nil mux client")
	}
	if strings.TrimSpace(targetAddr) == "" {
		return nil, fmt.Errorf("target address cannot be empty")
	}

	sess, err := c.getOrCreateSession(ctx)
	if err != nil {
		return nil, err
	}

	conn, err := dialMuxStream(sess, targetAddr)
	if err == nil {
		return conn, nil
	}

	// One retry on a potentially stale session.
	c.resetSession()
	sess, err2 := c.getOrCreateSession(ctx)
	if err2 != nil {
		return nil, err
	}
	return dialMuxStream(sess, targetAddr)
}

func (c *MuxClient) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	sess := c.session
	c.session = nil
	c.creating = false
	if c.cond != nil {
		c.cond.Broadcast()
	}
	c.mu.Unlock()
	if sess != nil {
		sess.closeWithError(io.ErrClosedPipe)
	}
	return nil
}

func (c *MuxClient) resetSession() {
	c.mu.Lock()
	sess := c.session
	c.session = nil
	c.mu.Unlock()
	if sess != nil {
		sess.closeWithError(io.ErrClosedPipe)
	}
}

func (c *MuxClient) getOrCreateSession(ctx context.Context) (*muxSession, error) {
	c.mu.Lock()
	if c.cond == nil {
		c.cond = sync.NewCond(&c.mu)
	}
	for {
		if sess := c.session; sess != nil && !sess.isClosed() {
			c.mu.Unlock()
			return sess, nil
		}
		if !c.creating {
			c.creating = true
			break
		}
		c.cond.Wait()
	}
	c.mu.Unlock()

	baseConn, err := c.dialBase(ctx)
	if err != nil {
		c.mu.Lock()
		c.creating = false
		c.cond.Broadcast()
		c.mu.Unlock()
		return nil, err
	}

	if err := WriteMuxPreface(baseConn); err != nil {
		_ = baseConn.Close()
		c.mu.Lock()
		c.creating = false
		c.cond.Broadcast()
		c.mu.Unlock()
		return nil, fmt.Errorf("mux preface failed: %w", err)
	}

	createdSession := newMuxSession(baseConn, nil)

	c.mu.Lock()
	c.session = createdSession
	c.creating = false
	c.cond.Broadcast()
	c.mu.Unlock()
	return createdSession, nil
}

func dialMuxStream(sess *muxSession, targetAddr string) (net.Conn, error) {
	if sess == nil {
		return nil, fmt.Errorf("nil mux session")
	}
	if sess.isClosed() {
		return nil, sess.closedErr()
	}

	addrBytes, err := EncodeAddress(targetAddr)
	if err != nil {
		return nil, fmt.Errorf("encode address failed: %w", err)
	}

	streamID := sess.nextStreamID()
	st := newMuxStream(sess, streamID)
	sess.registerStream(st)

	if err := sess.sendFrame(muxFrameOpen, streamID, addrBytes); err != nil {
		st.closeNoSend(err)
		sess.removeStream(streamID)
		return nil, fmt.Errorf("mux open failed: %w", err)
	}
	return st, nil
}

