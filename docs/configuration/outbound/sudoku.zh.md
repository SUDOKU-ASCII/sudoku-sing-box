### 结构

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
  "http_mask_strategy": "random",
  "httpmask": {
    "disable": false,
    "mode": "legacy",
    "tls": false,
    "host": "",
    "path_root": "",
    "multiplex": "off"
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

  ... // 拨号字段
}
```

### 字段

#### server

==必填==

服务器地址。

#### server_port

==必填==

服务器端口。

#### key

==必填==

Sudoku 密钥。

可以是共享密钥字符串（两端填写相同值），也可以是 Ed25519 密钥。

如需使用 Ed25519 密钥对模式，可通过 `sing-box generate sudoku-keypair` 生成，然后将出站 `key` 设置为 `PrivateKey`，入站 `key` 设置为 `PublicKey`。

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

#### httpmask.disable

禁用所有 HTTP 伪装层。

仍兼容旧版平铺字段 `disable_http_mask` / `http_mask_*`，但推荐使用 `httpmask`。

#### httpmask.mode

HTTP 伪装模式。

可选值：

* `legacy`（写入伪造 HTTP 头，无法通过 CDN）
* `stream`（真实 HTTP 流式隧道，可通过 CDN）
* `poll`（真实 HTTP 轮询隧道）
* `auto`（先尝试 stream，失败后回退到 poll）

#### httpmask.multiplex

当 `httpmask.mode` 为 `stream`/`poll`/`auto` 时的复用行为。

可选值：

* `off`（禁用传输复用与 mux）
* `auto`（复用底层 HTTP 连接；在 HTTP/2 下可多路复用多个隧道）
* `on`（单隧道多目标 mux：在一个 HTTPMask 隧道中复用多个目标连接，降低每连接 RTT）

#### httpmask.tls

为 `httpmask.mode` 为 `stream`/`poll`/`auto` 时启用 HTTPS。

#### httpmask.host

为 `httpmask.mode` 为 `stream`/`poll`/`auto` 时覆盖 HTTP Host 头 / SNI Host。

#### httpmask.path_root

为所有 HTTP mask 端点增加一级路径前缀（可选）。

例如：`aabbcc` => `/aabbcc/session`、`/aabbcc/api/v1/upload` ...

#### http_mask_strategy

`httpmask.mode` 为 `legacy` 时使用的 HTTP 头模板策略。

可选值：

* `random`
* `post`
* `websocket`

#### reverse.client_id

可选的反向代理客户端标识。

#### reverse.routes

需要通过服务端 `reverse.listen` 入口暴露的客户端侧服务。

每条路由字段：

* `path`：公开 HTTP 路径前缀。为空时表示原始 TCP 反向转发。
* `target`：客户端侧 `host:port` 目标。
* `strip_prefix`：代理到上游前是否剥离公开路径，默认 `true`。
* `host_header`：可选的上游 Host 头覆盖。

反向代理会使用 packed uplink，与官方 Sudoku v0.4.6 的反代行为一致。

### 拨号字段

参阅 [拨号字段](/zh/configuration/shared/dial/)。
