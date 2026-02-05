package sudoku

import "net"

// SuspiciousError indicates a potential attack or protocol violation.
//
// Conn carries the connection at the state where the error occurred, primarily for defensive fallback.
// It should implement GetBufferedAndRecorded() when possible.
type SuspiciousError struct {
	Err  error
	Conn net.Conn
}

func (e *SuspiciousError) Error() string {
	if e == nil || e.Err == nil {
		return "suspicious error"
	}
	return e.Err.Error()
}

func (e *SuspiciousError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type recordedConn struct {
	net.Conn
	recorded []byte
}

func (rc *recordedConn) GetBufferedAndRecorded() []byte {
	if rc == nil || len(rc.recorded) == 0 {
		return nil
	}
	out := make([]byte, len(rc.recorded))
	copy(out, rc.recorded)
	return out
}
