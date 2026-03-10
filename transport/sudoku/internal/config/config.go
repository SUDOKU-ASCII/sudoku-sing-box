package config

import "strings"

type HTTPMaskConfig struct {
	Disable   bool
	Mode      string
	TLS       bool
	Host      string
	PathRoot  string
	Multiplex string
}

type Config struct {
	ServerAddress      string
	Key                string
	AEAD               string
	PaddingMin         int
	PaddingMax         int
	EnablePureDownlink bool
	HTTPMask           HTTPMaskConfig
	SuspiciousAction   string
	FallbackAddr       string
}

func (c *Config) HTTPMaskTunnelEnabled() bool {
	if c == nil || c.HTTPMask.Disable {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(c.HTTPMask.Mode)) {
	case "stream", "poll", "auto", "ws":
		return true
	default:
		return false
	}
}
