package remote

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/coder/websocket"
	"github.com/pencode-labs/pencode/transport"
)

type WebSocketTarget struct {
	Address string `json:"address"`
	Token   string `json:"token"`
}

func (t WebSocketTarget) Validate() error {
	u, err := url.Parse(t.Address)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return fmt.Errorf("请输入有效的 ws:// 或 wss:// 地址（不包含凭据、查询参数或片段）")
	}
	return ValidateToken(t.Token)
}

// ValidateToken restricts tokens to visible ASCII so they travel unchanged in
// the Authorization header. Tokens are never included in URLs or diagnostics.
func ValidateToken(token string) error {
	if token == "" {
		return fmt.Errorf("请输入连接 token")
	}
	for i := range len(token) {
		if token[i] < '!' || token[i] > '~' {
			return fmt.Errorf("token 只能包含不带空格的可见 ASCII 字符")
		}
	}
	return nil
}

func ConnectWebSocket(ctx context.Context, target WebSocketTarget, progress func(string)) (io.ReadWriteCloser, error) {
	if err := target.Validate(); err != nil {
		return nil, err
	}
	if progress != nil {
		progress("正在连接 WebSocket 服务…")
	}
	conn, response, err := websocket.Dial(ctx, target.Address, &websocket.DialOptions{
		HTTPClient: webSocketHTTPClient,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + target.Token}},
	})
	if err != nil {
		if response != nil && response.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("WebSocket 握手被拒绝，请检查服务地址和 token")
		}
		return nil, err
	}
	return transport.NewWebSocket(conn), nil
}

// Credentials belong only to the configured endpoint, never a redirect target.
var webSocketHTTPClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}}
