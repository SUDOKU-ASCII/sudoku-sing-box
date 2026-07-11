### Structure

```json
{
  "type": "sudoku",
  "tag": "sudoku-out",

  "server": "127.0.0.1",
  "server_port": 1080,
  "key": "test_key",
  "aead": "chacha20-poly1305",
  "padding_min": 10,
  "padding_max": 30,
  "ascii": "prefer_ascii",
  "custom_table": "",
  "custom_tables": [],
  "enable_pure_downlink": true,
  "multiplex": "off",
  "http_mask_strategy": "random",
  "httpmask": {
    "disable": false,
    "mode": "legacy",
    "tls": false,
    "host": "",
    "path_root": ""
  },
  "reverse": {
    "client_id": "client-a",
    "routes": [
      {
        "path": "/app",
        "target": "127.0.0.1:3000",
        "strip_prefix": true,
        "host_header": ""
      }
    ]
  },

  ... // Dial Fields
}
```

### Fields

#### server

==Required==

The server address.

#### server_port

==Required==

The server port.

#### key

==Required==

Sudoku key.

It can be a shared secret string (same value on both sides) or an Ed25519 key.

To use Ed25519 key pair mode, generate one with `sing-box generate sudoku-keypair`, then set outbound `key` to `PrivateKey` and inbound `key` to `PublicKey`.

#### aead

AEAD method.

Available values:

* `aes-128-gcm`
* `chacha20-poly1305`
* `none`

#### padding_min

Padding min rate (0-100).

#### padding_max

Padding max rate (0-100).

#### ascii

Table mode.

Available values:

* `prefer_ascii`
* `prefer_entropy`

#### custom_table

Custom table pattern for entropy mode.

It must contain 8 symbols, exactly 2 `x`, 2 `p` and 4 `v`, e.g. `xpxvvpvv`.

Ignored when `ascii` is `prefer_ascii`.

#### custom_tables

Custom table patterns (rotation).

When enabled, both client and server must provide the same pattern list.

#### enable_pure_downlink

Enable pure downlink mode.

Set to `false` to use packed downlink mode (requires AEAD, `aead: none` is not allowed).

#### httpmask.disable

Disable all HTTP masking layers.

Legacy flat fields `disable_http_mask` / `http_mask_*` are still accepted for compatibility, but `httpmask` is recommended.

#### httpmask.mode

HTTP masking mode.

Available values:

* `legacy` (write a fake HTTP header, not CDN compatible)
* `stream` (real HTTP streaming tunnel, CDN compatible)
* `poll` (real HTTP polling tunnel)
* `auto` (try stream then fall back to poll)

#### multiplex

Sudoku session multiplex and HTTPMask transport reuse mode.

Available values:

* `off` (disable session mux and HTTPMask transport reuse)
* `auto` (reuse underlying HTTP connections across HTTPMask tunnel dials)
* `on` (reuse one raw TCP or HTTPMask Sudoku session for multiple target streams)

The legacy `httpmask.multiplex` and flat `http_mask_multiplex` fields are still accepted. When both a legacy field and `multiplex` are set, the legacy value takes precedence.

#### httpmask.tls

Enable HTTPS for `httpmask.mode` `stream`/`poll`/`auto`.

#### httpmask.host

Override HTTP Host header / SNI host for `httpmask.mode` `stream`/`poll`/`auto`.

#### httpmask.path_root

Optional first-level path prefix for all HTTP mask endpoints.

Example: `aabbcc` => `/aabbcc/session`, `/aabbcc/api/v1/upload`, ...

#### http_mask_strategy

HTTP header template strategy for `httpmask.mode` `legacy`.

Available values:

* `random`
* `post`
* `websocket`

#### reverse.client_id

Optional reverse client identifier.

#### reverse.routes

Client-side services to expose through a server-side `reverse.listen` entry.

Each route uses:

* `path`: public HTTP path prefix. Empty path means raw TCP reverse forwarding.
* `target`: client-side `host:port` target.
* `strip_prefix`: strip the public path before proxying. Defaults to `true`.
* `host_header`: optional upstream Host header override.

Reverse sessions use packed uplink, matching official Sudoku v0.4.6 reverse behavior.

### Dial Fields

See [Dial Fields](/configuration/shared/dial/) for details.
