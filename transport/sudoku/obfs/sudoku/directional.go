package sudoku

import (
	"io"
	"net"
)

// DirectionalConn wires separate reader/writer streams onto a single net.Conn.
// It is useful for asymmetric obfuscation (e.g. packed downlink, sudoku uplink).
type DirectionalConn struct {
	net.Conn
	reader  io.Reader
	writer  io.Writer
	closers []func() error
}

func NewDirectionalConn(base net.Conn, reader io.Reader, writer io.Writer, closers ...func() error) *DirectionalConn {
	return &DirectionalConn{
		Conn:    base,
		reader:  reader,
		writer:  writer,
		closers: closers,
	}
}

func (c *DirectionalConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func (c *DirectionalConn) Write(p []byte) (int, error) {
	return c.writer.Write(p)
}

func (c *DirectionalConn) CloseRead() error {
	if err := tryCloseRead(c.reader); err != nil {
		return err
	}
	return tryCloseRead(c.Conn)
}

func (c *DirectionalConn) CloseWrite() error {
	firstErr := runClosers(c.closers...)
	if err := tryCloseWrite(c.writer); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := tryCloseWrite(c.Conn); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (c *DirectionalConn) Close() error {
	firstErr := runClosers(c.closers...)
	if err := c.Conn.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func runClosers(closers ...func() error) error {
	var firstErr error
	for _, fn := range closers {
		if fn == nil {
			continue
		}
		if err := fn(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func tryCloseRead(v any) error {
	if v == nil {
		return nil
	}
	if cr, ok := v.(interface{ CloseRead() error }); ok {
		return cr.CloseRead()
	}
	if c, ok := v.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func tryCloseWrite(v any) error {
	if v == nil {
		return nil
	}
	if cw, ok := v.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	if c, ok := v.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
