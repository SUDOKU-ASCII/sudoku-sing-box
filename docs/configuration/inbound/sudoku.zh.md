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
  },
  "users": [
    {
      "alias": "alice",
      "key": "<客户端私钥，或其用户哈希的 8-16 位十六进制前缀>",
      "bandwidth": [100, 20],
      "data": 50,
      "blocked": false
    }
  ]
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

#### users

用户白名单。非空时，只有握手中的用户哈希命中某个条目的客户端才能建立会话，握手成功但不在名单内的连接会被断开。留空则接受所有持有 key 的客户端。

用户哈希为 `sha256(私钥)[:8]` 的十六进制，由客户端在握手中发送。它不由服务端 `key` 派生，因此每个客户端需要各自的条目：用 `sing-box generate sudoku-keypair --more <PrivateKey>` 为同一公钥派生更多私钥，再填到这里。

条目格式与官方 Sudoku 服务端一致：

* `key`：==必填== 客户端私钥（64 或 128 位十六进制），或其用户哈希的前缀（8–16 位十六进制），按长度区分。建议使用哈希前缀，这样服务端不必保存私钥。
* `alias`：日志中显示的名称，默认为用户哈希，不会是 key。
* `bandwidth`：`[服务端到客户端, 客户端到服务端]` 的负载速率限制，单位 Mbps，`0` 表示不限速。
* `data`：双向 wire 流量总配额，单位 GB。用尽后断开现有连接并拒绝新连接，直到入站重启，`0` 表示不限。
* `blocked`：拒绝该用户，同时保留条目。
