/*
Copyright (C) 2026 by saba <contact me via issue>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.

In addition, no derivative work may use the name or imply association
with this application without prior consent.
*/
package httpmask

import (
	"net"
	"sync"
	"time"

	"github.com/sagernet/sing-box/transport/sudoku/connutil"
)

const minReapInterval = 50 * time.Millisecond

// tunnelSession is the server side of one stream/poll session: the pipe that
// carries tunnel bytes plus the bookkeeping HTTP requests need to share it.
type tunnelSession struct {
	conn           *halfPipeConn
	lastActive     time.Time   // guarded by sessionStore.mu
	uplinkClosed   bool        // guarded by sessionStore.mu
	downlinkClosed bool        // guarded by sessionStore.mu
	reaper         *time.Timer // set under sessionStore.mu before the session is visible

	uploadMu      sync.Mutex
	nextUploadSeq uint64

	// Lock order: pullMu before sessionStore.mu.
	pullMu          sync.Mutex
	pull            *sessionPullLease
	pendingDownlink []byte // owned by the current pull lease holder
}

func newTunnelSession(conn *halfPipeConn) *tunnelSession {
	return &tunnelSession{conn: conn, lastActive: time.Now(), nextUploadSeq: 1}
}

// shutdown disarms the reaper, stops the active pull and closes the tunnel pipe.
func (sess *tunnelSession) shutdown() {
	sess.reaper.Stop()
	sess.pullMu.Lock()
	lease := sess.pull
	sess.pullMu.Unlock()
	lease.stop()
	_ = sess.conn.Close()
}

func (sess *tunnelSession) endPull(lease *sessionPullLease) {
	sess.pullMu.Lock()
	if sess.pull == lease {
		sess.pull = nil
	}
	sess.pullMu.Unlock()
	close(lease.done)
}

// sessionPullLease is the exclusive right to read a session's downlink.
type sessionPullLease struct {
	cancel   chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	rawConn  net.Conn
}

func newSessionPullLease(rawConn net.Conn) *sessionPullLease {
	return &sessionPullLease{
		cancel:  make(chan struct{}),
		done:    make(chan struct{}),
		rawConn: rawConn,
	}
}

func (p *sessionPullLease) stop() {
	if p != nil {
		p.stopOnce.Do(func() {
			close(p.cancel)
			if p.rawConn != nil {
				_ = p.rawConn.Close()
			}
		})
	}
}

// sessionStore holds the live sessions of one TunnelServer and reaps idle ones.
type sessionStore struct {
	mu      sync.Mutex
	ttl     time.Duration
	byToken map[string]*tunnelSession
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{ttl: ttl, byToken: make(map[string]*tunnelSession)}
}

// add registers a session for conn and arms its idle reaper.
func (st *sessionStore) add(token string, conn *halfPipeConn) *tunnelSession {
	sess := newTunnelSession(conn)
	st.mu.Lock()
	st.byToken[token] = sess
	sess.reaper = time.AfterFunc(st.ttl, func() { st.reap(token, sess) })
	st.mu.Unlock()
	return sess
}

func (st *sessionStore) len() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.byToken)
}

func (st *sessionStore) has(token string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	_, ok := st.byToken[token]
	return ok
}

// get returns the session and marks it active.
func (st *sessionStore) get(token string) (*tunnelSession, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	sess, ok := st.byToken[token]
	if !ok {
		return nil, false
	}
	sess.lastActive = time.Now()
	return sess, true
}

// touch marks sess active, reporting false if it was closed or replaced meanwhile.
func (st *sessionStore) touch(token string, sess *tunnelSession) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.byToken[token] != sess {
		return false
	}
	sess.lastActive = time.Now()
	return true
}

func (st *sessionStore) close(token string) {
	st.mu.Lock()
	sess, ok := st.byToken[token]
	if ok {
		delete(st.byToken, token)
	}
	st.mu.Unlock()
	if ok {
		sess.shutdown()
	}
}

// closeUplink records the client's FIN: the tunnel sees EOF on its read side.
func (st *sessionStore) closeUplink(token string) { st.halfClose(token, true) }

// closeDownlink records upstream EOF delivered to the client.
func (st *sessionStore) closeDownlink(token string) { st.halfClose(token, false) }

func (st *sessionStore) halfClose(token string, uplink bool) {
	var closeWrite, removed bool

	st.mu.Lock()
	sess, ok := st.byToken[token]
	if !ok {
		st.mu.Unlock()
		return
	}
	sess.lastActive = time.Now()
	if uplink {
		if !sess.uplinkClosed {
			sess.uplinkClosed = true
			closeWrite = true
		}
	} else {
		sess.downlinkClosed = true
	}
	if sess.uplinkClosed && sess.downlinkClosed {
		delete(st.byToken, token)
		removed = true
	}
	st.mu.Unlock()

	if closeWrite {
		_ = connutil.TryCloseWrite(sess.conn)
	}
	if removed {
		sess.reaper.Stop()
		_ = sess.conn.Close()
	}
}

// beginPull grants rawConn the session's pull lease, evicting a previous holder.
func (st *sessionStore) beginPull(token string, sess *tunnelSession, rawConn net.Conn) (*sessionPullLease, bool) {
	for {
		sess.pullMu.Lock()
		if !st.touch(token, sess) {
			sess.pullMu.Unlock()
			return nil, false
		}
		previous := sess.pull
		if previous == nil {
			lease := newSessionPullLease(rawConn)
			sess.pull = lease
			sess.pullMu.Unlock()
			return lease, true
		}
		previous.stop()
		sess.pullMu.Unlock()

		// Only one goroutine may read a session pipe. A new pull takes over a
		// stale CDN response by waking its pending read before it starts, and
		// re-checks because a concurrent newcomer may win the freed slot first.
		_ = sess.conn.SetReadDeadline(time.Now())
		<-previous.done
	}
}

// reap runs when the reaper fires and re-arms it unless sess is gone.
func (st *sessionStore) reap(token string, sess *tunnelSession) {
	next, done := st.reapIfIdle(token, sess)
	if done {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.byToken[token] == sess {
		// Best-effort cleanup does not need a tight loop under constant activity.
		sess.reaper.Reset(max(next, minReapInterval))
	}
}

// reapIfIdle closes sess once it has been idle for the TTL with no active pull,
// otherwise it returns how long to wait before checking again.
func (st *sessionStore) reapIfIdle(token string, sess *tunnelSession) (next time.Duration, done bool) {
	st.mu.Lock()
	if st.byToken[token] != sess {
		st.mu.Unlock()
		return 0, true
	}
	idle := time.Since(sess.lastActive)
	st.mu.Unlock()

	// Do not take pullMu while holding st.mu: pull takeover validates the
	// session while holding pullMu.
	sess.pullMu.Lock()
	pulling := sess.pull != nil
	sess.pullMu.Unlock()

	st.mu.Lock()
	if st.byToken[token] != sess {
		st.mu.Unlock()
		return 0, true
	}
	if idle >= st.ttl && !pulling {
		delete(st.byToken, token)
		st.mu.Unlock()
		sess.shutdown()
		return 0, true
	}
	next = st.ttl - idle
	if pulling && next < st.ttl {
		next = st.ttl
	}
	st.mu.Unlock()
	return next, false
}
