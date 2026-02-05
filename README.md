# sing-box( sudoku feat. )

The universal proxy platform.

## Documentation

https://sing-box.sagernet.org

## Sudoku interop testing

- Official repo: https://github.com/SUDOKU-ASCII/sudoku
- Run: `SUDOKU_OFFICIAL_BIN=/path/to/sudoku SUDOKU_OFFICIAL_INTEROP=1 go test ./protocol/sudoku -run TestSudoku_OfficialInterop -count=1`
- Or: `bash scripts/sudoku_official_interop.sh`
- UDP quick check (via outbound): `./sing-box tools dnsquery 8.8.8.8:53 example.com -c <config.json> -o <outbound-tag>`

## Sudoku 使用说明

### 1) 生成 Key（可选）

- 共享密钥模式：客户端与服务端 `key` 使用同一字符串即可。
- 公私钥（split-key）模式：客户端用 `PrivateKey`，服务端用 `PublicKey`（兼容官方 Sudoku 行为）。

生成一组公私钥：

`sing-box generate sudoku-keypair`

### 2) Outbound（客户端）配置

`type: "sudoku"`，常用字段：

- `server` / `server_port`
- `key`
- `aead`: `"chacha20-poly1305"`（默认）/ `"aes-128-gcm"` / `"none"`（仅测试）
- `ascii`: `"prefer_ascii"` / `"prefer_entropy"`
- `custom_table` 或 `custom_tables`: 例如 `"xpxvvpvv"`（2 个 `x`、2 个 `p`、4 个 `v`）
- `padding_min` / `padding_max`: 0–100
- `enable_pure_downlink`: `false` 会启用带宽优化下行（要求 `aead != "none"`）
- `httpmask`: 推荐的 HTTPMask 配置对象（见下方示例）
- 兼容旧字段：`disable_http_mask` / `http_mask_*` 仍可用
- `http_mask_strategy`: 仅影响 `httpmask.mode=legacy` 的伪装头生成（`random`/`post`/`websocket`）

示例（CDN/反代场景，HTTPS + auto）：

```json
{
  "type": "sudoku",
  "tag": "sudoku-out",
  "server": "example.com",
  "server_port": 443,
  "key": "YOUR_PRIVATE_KEY_OR_SHARED_KEY",
  "aead": "chacha20-poly1305",
  "ascii": "prefer_entropy",
  "custom_table": "xpxvvpvv",
  "padding_min": 5,
  "padding_max": 15,
  "enable_pure_downlink": true,
  "httpmask": {
    "disable": false,
    "mode": "auto",
    "tls": true,
    "host": "example.com",
    "path_root": "aabbcc",
    "multiplex": "auto"
  }
}
```

### 3) Inbound（服务端）配置

`type: "sudoku"`，常用字段：

- `listen` / `listen_port`
- `key`（公私钥模式下填 `PublicKey`）
- `handshake_timeout`（秒）
- `fallback_address` / `suspicious_action`：可疑连接诱饵回落（服务端）
- 其余字段与 outbound 同名字段含义一致：`aead`/`padding_*`/`ascii`/`custom_table(s)`/`enable_pure_downlink`/`httpmask`（或旧版 `disable_http_mask` / `http_mask_*`）

示例（与上面 outbound 对应）：

```json
{
  "type": "sudoku",
  "tag": "sudoku-in",
  "listen": "0.0.0.0",
  "listen_port": 8080,
  "key": "YOUR_PUBLIC_KEY_OR_SHARED_KEY",
  "aead": "chacha20-poly1305",
  "ascii": "prefer_entropy",
  "custom_table": "xpxvvpvv",
  "padding_min": 5,
  "padding_max": 15,
  "enable_pure_downlink": true,
  "handshake_timeout": 5,
  "fallback_address": "127.0.0.1:80",
  "suspicious_action": "fallback",
  "httpmask": {
    "disable": false,
    "mode": "auto",
    "path_root": "aabbcc",
    "multiplex": "auto"
  }
}
```

### 4) UDP（UoT：UDP over TCP）

Sudoku outbound 的 UDP 通过 `listen_packet`（内部走 UoT）提供；可用命令快速验证：

`sing-box tools dnsquery 8.8.8.8:53 example.com -c <config.json> -o <sudoku-out-tag>`

## License

```
Copyright (C) 2022 by nekohasekai <contact-sagernet@sekai.icu>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.

In addition, no derivative work may use the name or imply association
with this application without prior consent.
```
