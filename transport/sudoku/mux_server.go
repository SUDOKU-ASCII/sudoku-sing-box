package sudoku

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
)

// HandleMuxServer serves a multiplexed Sudoku tunnel connection after the mux preface has been consumed.
//
// Wire format:
//   - [MuxMagicByte][muxVersion] (both are consumed by the caller)
//   - then mux frames (open/data/close/reset)
func HandleMuxServer(conn net.Conn, onStream func(stream net.Conn, targetAddr string)) error {
	if conn == nil {
		return fmt.Errorf("nil conn")
	}
	if onStream == nil {
		return fmt.Errorf("nil onStream")
	}

	sess := newMuxSession(conn, func(stream *muxStream, payload []byte) {
		sess := stream.session
		addr, err := decodeMuxOpenTarget(payload)
		if err != nil {
			sess.sendReset(stream.id, "bad address")
			stream.closeNoSend(err)
			sess.removeStream(stream.id)
			return
		}

		onStream(stream, addr)
	})

	<-sess.closed
	err := sess.closedErr()
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func decodeMuxOpenTarget(payload []byte) (string, error) {
	addr, err := DecodeAddress(bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	if addr == "" {
		return "", fmt.Errorf("empty address")
	}
	return addr, nil
}

