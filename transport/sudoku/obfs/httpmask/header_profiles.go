package httpmask

// Header profiles are shared by the legacy prelude and HTTP/WebSocket tunnels.
// Keep the values and their order stable: seeded selection and existing clients
// use the same profiles regardless of the transport mode.
var (
	userAgents = [...]string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Safari/605.1.15",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_2_1) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Safari/605.1.15",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Mobile/15E148 Safari/604.1",
		"Mozilla/5.0 (Linux; Android 14; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Mobile Safari/537.36",
	}
	accepts = [...]string{
		"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		"application/json, text/plain, */*",
		"application/octet-stream",
		"*/*",
	}
	acceptLanguages = [...]string{
		"en-US,en;q=0.9",
		"en-GB,en;q=0.9",
		"zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7",
		"ja-JP,ja;q=0.9,en-US;q=0.8,en;q=0.7",
		"de-DE,de;q=0.9,en-US;q=0.8,en;q=0.7",
	}
	acceptEncodings = [...]string{
		"gzip, deflate, br",
		"gzip, deflate",
		"br, gzip, deflate",
	}
	paths = [...]string{
		"/api/v1/upload",
		"/data/sync",
		"/uploads/raw",
		"/api/report",
		"/feed/update",
		"/v2/events",
		"/v1/telemetry",
		"/session",
		"/stream",
		"/ws",
	}
	contentTypes = [...]string{
		"application/octet-stream",
		"application/x-protobuf",
		"application/json",
	}
)
