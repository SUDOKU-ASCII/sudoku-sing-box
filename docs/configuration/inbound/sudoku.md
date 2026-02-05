### Structure

```json
{
  "type": "sudoku",
  "tag": "sudoku-in",

  ... // Listen Fields

  "key": "test_key",
  "aead": "chacha20-poly1305",
  "padding_min": 10,
  "padding_max": 30,
  "ascii": "prefer_ascii",
  "custom_table": "",
  "custom_tables": [],
  "enable_pure_downlink": true,
  "handshake_timeout": 5,
  "fallback_address": "127.0.0.1:80",
  "suspicious_action": "fallback",
  "httpmask": {
    "disable": false,
    "mode": "legacy",
    "path_root": "",
    "multiplex": "off"
  }
}
```

### Listen Fields

See [Listen Fields](/configuration/shared/listen/) for details.

### Fields

#### key

==Required==

Sudoku key.

It can be a shared secret string (same value on both sides) or an Ed25519 key.

To use Ed25519 key pair mode, generate one with `sing-box generate sudoku-keypair`, then set inbound `key` to `PublicKey` and outbound `key` to `PrivateKey`.

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

#### handshake_timeout

Handshake timeout in seconds.

#### fallback_address

Decoy address (`host:port`) for suspicious connections.

Used when `suspicious_action` is `fallback`.

#### suspicious_action

Action for suspicious connections (server-side).

Available values:

* `fallback` (default)
* `silent` (tarpit, drop)

#### httpmask.disable

Disable all HTTP masking layers.

Legacy flat fields `disable_http_mask` / `http_mask_*` are still accepted for compatibility, but `httpmask` is recommended.

#### httpmask.mode

HTTP masking mode.

Available values:

* `legacy` (write a fake HTTP header, not CDN compatible)
* `stream` (real HTTP streaming tunnel, CDN compatible)
* `poll` (real HTTP polling tunnel)
* `auto` (accept stream and poll)

#### httpmask.multiplex

Client-side multiplex behavior for `httpmask.mode` `stream`/`poll`/`auto`.

Available values:

* `off`
* `auto`
* `on`

#### httpmask.path_root

Optional first-level path prefix for all HTTP mask endpoints.

Example: `aabbcc` => `/aabbcc/session`, `/aabbcc/api/v1/upload`, ...

Must match the client when `httpmask.mode` is `stream`/`poll`/`auto`.
