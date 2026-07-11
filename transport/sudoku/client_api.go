package sudoku

import (
	"context"
	"fmt"
	"net"

	internaltunnel "github.com/sagernet/sing-box/transport/sudoku/internal/tunnel"
)

func newStandardDialer(ctx context.Context, cfg *ProtocolConfig, validate func(*ProtocolConfig) error) (*internaltunnel.StandardDialer, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return &internaltunnel.StandardDialer{BaseDialer: *newBaseDialer(ctx, cfg)}, nil
}

func Dial(ctx context.Context, cfg *ProtocolConfig) (net.Conn, error) {
	dialer, err := newStandardDialer(ctx, cfg, (*ProtocolConfig).ValidateClient)
	if err != nil {
		return nil, err
	}
	return dialer.Dial(cfg.TargetAddress)
}

func DialUDPOverTCP(ctx context.Context, cfg *ProtocolConfig) (net.Conn, error) {
	dialer, err := newStandardDialer(ctx, cfg, (*ProtocolConfig).Validate)
	if err != nil {
		return nil, err
	}
	return dialer.DialUDPOverTCP()
}

type MuxClient struct {
	dialer *internaltunnel.MuxDialer
}

func NewMuxClient(cfg *ProtocolConfig) (*MuxClient, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &MuxClient{dialer: &internaltunnel.MuxDialer{BaseDialer: *newBaseDialer(context.Background(), cfg)}}, nil
}

func (c *MuxClient) Dial(ctx context.Context, targetAddr string) (net.Conn, error) {
	if c == nil || c.dialer == nil {
		return nil, fmt.Errorf("nil mux client")
	}
	return c.dialer.DialContext(ctx, targetAddr)
}

func (c *MuxClient) Warm(ctx context.Context) error {
	if c == nil || c.dialer == nil {
		return fmt.Errorf("nil mux client")
	}
	return c.dialer.Warm(ctx)
}

func (c *MuxClient) Maintain(ctx context.Context, notify func(error)) {
	if c == nil || c.dialer == nil {
		if notify != nil {
			notify(fmt.Errorf("nil mux client"))
		}
		return
	}
	c.dialer.Maintain(ctx, notify)
}

func (c *MuxClient) Close() error {
	if c == nil || c.dialer == nil {
		return nil
	}
	return c.dialer.Close()
}

func HandleMuxServer(conn net.Conn, onConnect func(stream net.Conn, targetAddr string)) error {
	return internaltunnel.HandleMuxWithStreamHandler(conn, onConnect)
}
