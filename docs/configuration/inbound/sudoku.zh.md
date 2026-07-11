### 结构

```json
{
  "type": "sudoku",
  "tag": "sudoku-in",

  ... // 监听字段

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
  }
}
```

### 监听字段

参阅 [监听字段](/zh/configuration/shared/listen/)。

### 字段

#### key

==必填==

Sudoku 密钥。

可以是共享密钥字符串（两端填写相同值），也可以是 Ed25519 密钥。

如需使用 Ed25519 密钥对模式，可通过 `sing-box generate sudoku-keypair` 生成，然后将入站 `key` 设置为 `PublicKey`，出站 `key` 设置为 `PrivateKey`。

#### aead

AEAD 方法。

可选值：

* `aes-128-gcm`
* `chacha20-poly1305`
* `none`

#### padding_min

填充最小比例（0-100）。

#### padding_max

填充最大比例（0-100）。

#### ascii

表模式。

可选值：

* `prefer_ascii`
* `prefer_entropy`

#### custom_table

entropy 模式的自定义表 pattern。

必须包含 8 个符号，且恰好 2 个 `x`、2 个 `p`、4 个 `v`，例如 `xpxvvpvv`。

当 `ascii` 为 `prefer_ascii` 时会被忽略。

#### custom_tables

自定义表 patterns（轮换）。

启用后，客户端与服务端必须提供相同的 pattern 列表。

#### enable_pure_downlink

启用 pure downlink 模式。

设为 `false` 会使用 packed downlink 模式（要求使用 AEAD，不能设置 `aead: none`）。

#### handshake_timeout

握手超时时间（秒）。

#### fallback_address

可疑连接的诱饵地址（`host:port`）。

当 `suspicious_action` 为 `fallback` 时使用。

#### suspicious_action

可疑连接处理方式（仅服务端）。

可选值：

* `fallback`（默认）
* `silent`（拖延/丢弃）

#### httpmask.disable

禁用所有 HTTP 伪装层。

仍兼容旧版平铺字段 `disable_http_mask` / `http_mask_*`，但推荐使用 `httpmask`。

#### httpmask.mode

HTTP 伪装模式。

可选值：

* `legacy`（写入伪造 HTTP 头，无法通过 CDN）
* `stream`（真实 HTTP 流式隧道，可通过 CDN）
* `poll`（真实 HTTP 轮询隧道）
* `auto`（同时接受 stream 与 poll）

#### multiplex

Sudoku 会话多路复用与 HTTPMask 传输复用模式。

可选值：

* `off`（禁用会话多路复用与 HTTPMask 传输复用）
* `auto`（在多个 HTTPMask 隧道拨号间复用底层 HTTP 连接）
* `on`（在一个原始 TCP 或 HTTPMask Sudoku 会话中复用多个目标流）

仍兼容旧字段 `httpmask.multiplex` 和平铺字段 `http_mask_multiplex`。同时设置旧字段与 `multiplex` 时，旧字段优先。

#### httpmask.path_root

为所有 HTTP mask 端点增加一级路径前缀（可选）。

例如：`aabbcc` => `/aabbcc/session`、`/aabbcc/api/v1/upload` ...

当 `httpmask.mode` 为 `stream`/`poll`/`auto` 时需与客户端一致。

#### reverse.listen

服务端反向代理入口地址。

设置后，反向代理客户端可以通过 Sudoku 隧道注册路由，该地址会作为公开反代入口。支持 HTTP 路径路由和一个原始 TCP 路由，行为与官方 Sudoku 反代保持一致。
