package sudoku

import (
	"context"
	"encoding/hex"
	"net"
	"strings"

	sudokuc "github.com/sagernet/sing-box/transport/sudoku/crypto"
	internalconfig "github.com/sagernet/sing-box/transport/sudoku/internal/config"
	internaltunnel "github.com/sagernet/sing-box/transport/sudoku/internal/tunnel"
	"github.com/sagernet/sing-box/transport/sudoku/obfs/httpmask"
)

type SessionKind int

const (
	SessionForward SessionKind = iota
	SessionUoT
	SessionMux
	SessionReverse
)

type ServerSession struct {
	Conn     net.Conn
	Type     SessionKind
	Target   string
	UserHash string
	Payload  []byte
}

type HTTPMaskTransportPool = httpmask.TransportPool
type SuspiciousError = internaltunnel.SuspiciousError

func toInternalConfig(cfg *ProtocolConfig) *internalconfig.Config {
	if cfg == nil {
		return nil
	}
	return &internalconfig.Config{
		ServerAddress:      cfg.ServerAddress,
		Key:                cfg.Key,
		AEAD:               cfg.AEADMethod,
		PaddingMin:         cfg.PaddingMin,
		PaddingMax:         cfg.PaddingMax,
		EnablePureDownlink: cfg.EnablePureDownlink,
		SuspiciousAction:   cfg.SuspiciousAction,
		FallbackAddr:       cfg.FallbackAddress,
		HTTPMask: internalconfig.HTTPMaskConfig{
			Disable:   cfg.DisableHTTPMask,
			Mode:      strings.ToLower(strings.TrimSpace(cfg.HTTPMaskMode)),
			TLS:       cfg.HTTPMaskTLSEnabled,
			Host:      cfg.HTTPMaskHost,
			PathRoot:  cfg.HTTPMaskPathRoot,
			Multiplex: strings.ToLower(strings.TrimSpace(cfg.HTTPMaskMultiplex)),
		},
	}
}

func privateKeyBytes(key string) []byte {
	if _, err := sudokuc.RecoverPublicKey(key); err != nil {
		return nil
	}
	keyBytes, err := hex.DecodeString(key)
	if err != nil {
		return nil
	}
	return keyBytes
}

func newBaseDialer(ctx context.Context, cfg *ProtocolConfig) *internaltunnel.BaseDialer {
	internalCfg := toInternalConfig(cfg)
	if internalCfg != nil {
		internalCfg.Key = clientTableSeed(cfg.Key)
	}
	return &internaltunnel.BaseDialer{
		Config:        internalCfg,
		Tables:        cfg.tableCandidates(),
		PrivateKey:    privateKeyBytes(cfg.Key),
		Context:       ctx,
		DialContext:   cfg.DialContext,
		TransportPool: cfg.HTTPMaskTransportPool,
	}
}
