package option

import (
	"bytes"
	"encoding/json"
	"strings"
)

type SudokuHTTPMaskOptions struct {
	Disable   bool   `json:"disable"`
	Mode      string `json:"mode,omitempty"`
	TLS       bool   `json:"tls,omitempty"`
	Host      string `json:"host,omitempty"`
	PathRoot  string `json:"path_root,omitempty"`
	Multiplex string `json:"multiplex,omitempty"`
}

type SudokuReverseOptions struct {
	Listen   string               `json:"listen,omitempty"`
	ClientID string               `json:"client_id,omitempty"`
	Routes   []SudokuReverseRoute `json:"routes,omitempty"`
}

type SudokuReverseRoute struct {
	Path        string `json:"path"`
	Target      string `json:"target"`
	StripPrefix *bool  `json:"strip_prefix,omitempty"`
	HostHeader  string `json:"host_header,omitempty"`
}

func unmarshalOptional(obj map[string]json.RawMessage, key string, out any) (bool, error) {
	raw, ok := obj[key]
	if !ok {
		return false, nil
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return false, nil
	}
	return true, json.Unmarshal(raw, out)
}

type SudokuInboundOptions struct {
	ListenOptions
	Key                string                `json:"key"`
	AEADMethod         string                `json:"aead,omitempty"`
	PaddingMin         *int                  `json:"padding_min,omitempty"`
	PaddingMax         *int                  `json:"padding_max,omitempty"`
	ASCII              string                `json:"ascii,omitempty"`
	CustomTable        string                `json:"custom_table,omitempty"`
	CustomTables       []string              `json:"custom_tables,omitempty"`
	EnablePureDownlink *bool                 `json:"enable_pure_downlink,omitempty"`
	HandshakeTimeout   int                   `json:"handshake_timeout,omitempty"`
	FallbackAddress    string                `json:"fallback_address,omitempty"`
	SuspiciousAction   string                `json:"suspicious_action,omitempty"`
	Multiplex          string                `json:"multiplex,omitempty"`
	DisableHTTPMask    bool                  `json:"disable_http_mask,omitempty"`
	HTTPMaskMode       string                `json:"http_mask_mode,omitempty"`
	HTTPMaskMultiplex  string                `json:"http_mask_multiplex,omitempty"`
	HTTPMaskPathRoot   string                `json:"http_mask_path_root,omitempty"`
	HTTPMask           SudokuHTTPMaskOptions `json:"httpmask,omitempty"`
	Reverse            SudokuReverseOptions  `json:"reverse,omitempty"`
}

type SudokuOutboundOptions struct {
	DialerOptions
	ServerOptions
	Key                string                `json:"key"`
	AEADMethod         string                `json:"aead,omitempty"`
	PaddingMin         *int                  `json:"padding_min,omitempty"`
	PaddingMax         *int                  `json:"padding_max,omitempty"`
	ASCII              string                `json:"ascii,omitempty"`
	CustomTable        string                `json:"custom_table,omitempty"`
	CustomTables       []string              `json:"custom_tables,omitempty"`
	EnablePureDownlink *bool                 `json:"enable_pure_downlink,omitempty"`
	Multiplex          string                `json:"multiplex,omitempty"`
	DisableHTTPMask    bool                  `json:"disable_http_mask,omitempty"`
	HTTPMaskMode       string                `json:"http_mask_mode,omitempty"`
	HTTPMaskMultiplex  string                `json:"http_mask_multiplex,omitempty"`
	HTTPMaskPathRoot   string                `json:"http_mask_path_root,omitempty"`
	HTTPMaskTLS        bool                  `json:"http_mask_tls,omitempty"`
	HTTPMaskHost       string                `json:"http_mask_host,omitempty"`
	HTTPMaskStrategy   string                `json:"http_mask_strategy,omitempty"`
	HTTPMask           SudokuHTTPMaskOptions `json:"httpmask,omitempty"`
	Reverse            SudokuReverseOptions  `json:"reverse,omitempty"`
}

func (o *SudokuInboundOptions) UnmarshalJSON(data []byte) error {
	type Alias SudokuInboundOptions
	if err := json.Unmarshal(data, (*Alias)(o)); err != nil {
		return err
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}

	var hm SudokuHTTPMaskOptions
	if ok, err := unmarshalOptional(obj, "httpmask", &hm); err != nil {
		return err
	} else if ok {
		o.DisableHTTPMask = hm.Disable
		o.HTTPMaskMode = hm.Mode
		if strings.TrimSpace(hm.Multiplex) != "" {
			o.HTTPMaskMultiplex = hm.Multiplex
		} else if strings.TrimSpace(o.HTTPMaskMultiplex) == "" {
			o.HTTPMaskMultiplex = o.Multiplex
		}
		o.HTTPMaskPathRoot = hm.PathRoot
		o.HTTPMask = hm
		return nil
	}

	if strings.TrimSpace(o.HTTPMaskMultiplex) == "" {
		o.HTTPMaskMultiplex = o.Multiplex
	}

	if strings.TrimSpace(o.HTTPMaskPathRoot) == "" {
		var pathRoot string
		if ok, err := unmarshalOptional(obj, "path_root", &pathRoot); err != nil {
			return err
		} else if ok {
			o.HTTPMaskPathRoot = pathRoot
		}
	}

	return nil
}

func (o *SudokuOutboundOptions) UnmarshalJSON(data []byte) error {
	type Alias SudokuOutboundOptions
	if err := json.Unmarshal(data, (*Alias)(o)); err != nil {
		return err
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}

	var hm SudokuHTTPMaskOptions
	if ok, err := unmarshalOptional(obj, "httpmask", &hm); err != nil {
		return err
	} else if ok {
		o.DisableHTTPMask = hm.Disable
		o.HTTPMaskMode = hm.Mode
		if strings.TrimSpace(hm.Multiplex) != "" {
			o.HTTPMaskMultiplex = hm.Multiplex
		} else if strings.TrimSpace(o.HTTPMaskMultiplex) == "" {
			o.HTTPMaskMultiplex = o.Multiplex
		}
		o.HTTPMaskTLS = hm.TLS
		o.HTTPMaskHost = hm.Host
		o.HTTPMaskPathRoot = hm.PathRoot
		o.HTTPMask = hm
		return nil
	}

	if strings.TrimSpace(o.HTTPMaskMultiplex) == "" {
		o.HTTPMaskMultiplex = o.Multiplex
	}

	if strings.TrimSpace(o.HTTPMaskPathRoot) == "" {
		var pathRoot string
		if ok, err := unmarshalOptional(obj, "path_root", &pathRoot); err != nil {
			return err
		} else if ok {
			o.HTTPMaskPathRoot = pathRoot
		}
	}

	return nil
}
