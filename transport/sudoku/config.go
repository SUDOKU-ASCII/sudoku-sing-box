package sudoku

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/sagernet/sing-box/transport/sudoku/obfs/httpmask"
	"github.com/sagernet/sing-box/transport/sudoku/obfs/sudoku"
)

type ProtocolConfig struct {
	ServerAddress string

	Key string

	AEADMethod string

	Table  *sudoku.Table
	Tables []*sudoku.Table

	PaddingMin int
	PaddingMax int

	EnablePureDownlink bool

	TargetAddress string

	HandshakeTimeoutSeconds int

	SuspiciousAction string
	FallbackAddress  string

	DisableHTTPMask bool
	HTTPMaskMode    string

	HTTPMaskTLSEnabled bool
	HTTPMaskHost       string
	HTTPMaskPathRoot   string
	HTTPMaskMultiplex  string

	DialContext           func(ctx context.Context, network, addr string) (net.Conn, error)
	HTTPMaskTransportPool *httpmask.TransportPool
}

func (c *ProtocolConfig) Validate() error {
	if c.Table == nil && len(c.Tables) == 0 {
		return fmt.Errorf("table cannot be nil (or provide tables)")
	}
	for i, t := range c.Tables {
		if t == nil {
			return fmt.Errorf("tables[%d] cannot be nil", i)
		}
	}

	if c.Key == "" {
		return fmt.Errorf("key cannot be empty")
	}

	switch c.AEADMethod {
	case "aes-128-gcm", "chacha20-poly1305", "none":
	default:
		return fmt.Errorf("invalid aead: %s, must be one of: aes-128-gcm, chacha20-poly1305, none", c.AEADMethod)
	}

	if c.PaddingMin < 0 || c.PaddingMin > 100 {
		return fmt.Errorf("padding_min must be between 0 and 100, got %d", c.PaddingMin)
	}
	if c.PaddingMax < 0 || c.PaddingMax > 100 {
		return fmt.Errorf("padding_max must be between 0 and 100, got %d", c.PaddingMax)
	}
	if c.PaddingMax < c.PaddingMin {
		return fmt.Errorf("padding_max (%d) must be >= padding_min (%d)", c.PaddingMax, c.PaddingMin)
	}

	if c.HandshakeTimeoutSeconds < 0 {
		return fmt.Errorf("handshake_timeout must be >= 0, got %d", c.HandshakeTimeoutSeconds)
	}

	switch strings.ToLower(strings.TrimSpace(c.SuspiciousAction)) {
	case "", "fallback", "silent":
	default:
		return fmt.Errorf("invalid suspicious_action: %s, must be one of: fallback, silent", c.SuspiciousAction)
	}
	if strings.ToLower(strings.TrimSpace(c.SuspiciousAction)) == "fallback" && strings.TrimSpace(c.FallbackAddress) != "" {
		if _, _, err := net.SplitHostPort(strings.TrimSpace(c.FallbackAddress)); err != nil {
			return fmt.Errorf("invalid fallback_address: %w", err)
		}
	}

	switch strings.ToLower(strings.TrimSpace(c.HTTPMaskMode)) {
	case "", "legacy", "stream", "poll", "auto", "ws":
	default:
		return fmt.Errorf("invalid http_mask_mode: %s, must be one of: legacy, stream, poll, auto, ws", c.HTTPMaskMode)
	}

	switch strings.ToLower(strings.TrimSpace(c.HTTPMaskMultiplex)) {
	case "", "off", "auto", "on":
	default:
		return fmt.Errorf("invalid http_mask_multiplex: %s, must be one of: off, auto, on", c.HTTPMaskMultiplex)
	}

	if v := strings.TrimSpace(c.HTTPMaskPathRoot); v != "" {
		v = strings.Trim(v, "/")
		if v == "" || strings.Contains(v, "/") {
			return fmt.Errorf("invalid http_mask_path_root: must be a single path segment")
		}
		for i := 0; i < len(v); i++ {
			ch := v[i]
			switch {
			case ch >= 'a' && ch <= 'z':
			case ch >= 'A' && ch <= 'Z':
			case ch >= '0' && ch <= '9':
			case ch == '_' || ch == '-':
			default:
				return fmt.Errorf("invalid http_mask_path_root: contains invalid character %q", ch)
			}
		}
	}

	return nil
}

func (c *ProtocolConfig) ValidateClient() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.ServerAddress == "" {
		return fmt.Errorf("server address cannot be empty")
	}
	if c.TargetAddress == "" {
		return fmt.Errorf("target address cannot be empty")
	}
	return nil
}

func DefaultConfig() *ProtocolConfig {
	return &ProtocolConfig{
		AEADMethod:              "chacha20-poly1305",
		PaddingMin:              10,
		PaddingMax:              30,
		EnablePureDownlink:      true,
		HandshakeTimeoutSeconds: 5,
		SuspiciousAction:        "fallback",
		HTTPMaskMode:            "legacy",
		HTTPMaskMultiplex:       "off",
	}
}

func (c *ProtocolConfig) tableCandidates() []*sudoku.Table {
	if c == nil {
		return nil
	}
	if len(c.Tables) > 0 {
		return c.Tables
	}
	if c.Table != nil {
		return []*sudoku.Table{c.Table}
	}
	return nil
}
