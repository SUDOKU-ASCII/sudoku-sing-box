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
  "multiplex": "off",
  "handshake_timeout": 5,
  "fallback_address": "127.0.0.1:80",
  "suspicious_action": "fallback",
  "httpmask": {
    "disable": false,
    "mode": "legacy",
    "path_root": ""
  },
  "reverse": {
    "listen": "127.0.0.1:8081"
  },
  "users": [
    {
      "alias": "alice",
      "key": "<client private key or 8-16 hex chars of its user hash>",
      "bandwidth": [100, 20],
      "data": 50,
      "blocked": false
    }
  ],
  "usage_file": ""
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

#### multiplex

Sudoku session multiplex and HTTPMask transport reuse mode.

Available values:

* `off` (disable session mux and HTTPMask transport reuse)
* `auto` (reuse underlying HTTP connections across HTTPMask tunnel dials)
* `on` (reuse one raw TCP or HTTPMask Sudoku session for multiple target streams)

The legacy `httpmask.multiplex` and flat `http_mask_multiplex` fields are still accepted. When both a legacy field and `multiplex` are set, the legacy value takes precedence.

#### httpmask.path_root

Optional first-level path prefix for all HTTP mask endpoints.

Example: `aabbcc` => `/aabbcc/session`, `/aabbcc/api/v1/upload`, ...

Must match the client when `httpmask.mode` is `stream`/`poll`/`auto`.

#### reverse.listen

Server-side reverse proxy entry address.

When set, reverse clients can register routes through the Sudoku tunnel and this address serves the public reverse entry. HTTP path routes and one raw TCP route are supported, matching the official Sudoku reverse behavior.

#### users

User whitelist. When non-empty, only clients whose handshake user hash matches an entry may open sessions; a client that completes the handshake but is not listed is dropped. Leave it empty to accept every client holding the key.

The user hash is `sha256(private key)[:8]` in hex, which the client sends in the handshake. It is not derived from the server `key`, so each client needs its own entry: derive more private keys for the same public key with `sing-box generate sudoku-keypair --more <PrivateKey>` and list them here.

Entries, in the same format as the official Sudoku server:

* `key`: ==Required== the client private key (64 or 128 hex characters), or a prefix of its user hash (8–16 hex characters). The two are told apart by length. Prefer a hash prefix so the private key does not have to be stored on the server.
* `alias`: Name shown in logs. Defaults to the user hash, never the key.
* `bandwidth`: `[server to client, client to server]` payload rate limit in Mbps. `0` means unlimited.
* `data`: Monthly wire traffic quota (both directions) in GB. Usage is counted per calendar month in server local time and resets automatically when a new month begins. Connections are closed when it is used up and new ones are refused until the next month. `0` means unlimited.
* `blocked`: Refuse this user without removing the entry.

Usage is saved every minute while it changes and once more on shutdown, and restored on start.
It is exposed by the Clash API at `GET /users` (`users[].used` / `users[].limit`).

#### usage_file

Path of the file that persists users' monthly usage across restarts.

`sudoku-{inbound tag}.usage.json` in the working directory will be used if empty, e.g.
`sudoku-sudoku-in.usage.json` for the tag `sudoku-in`. A file from an earlier month is ignored;
a corrupt file stops the start instead of silently resetting quotas.

The file is written atomically (temp file + rename). Delete it to reset all usage.
