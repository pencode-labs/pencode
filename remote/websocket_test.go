package remote

import "testing"

func TestWebSocketTargetValidation(t *testing.T) {
	for _, address := range []string{"ws://localhost:8080/ws", "wss://example.com/ws", "ws://[::1]:8080/ws"} {
		if err := (WebSocketTarget{Address: address, Token: "token-123"}).Validate(); err != nil {
			t.Fatalf("%s: %v", address, err)
		}
	}
	for _, target := range []WebSocketTarget{
		{Address: "http://localhost/ws", Token: "token"},
		{Address: "ws:///ws", Token: "token"},
		{Address: "ws://user:secret@localhost/ws", Token: "token"},
		{Address: "ws://localhost/ws?token=secret", Token: "token"},
		{Address: "ws://localhost/ws#fragment", Token: "token"},
		{Address: "ws://localhost/ws"},
		{Address: "ws://localhost/ws", Token: "header\r\ninjection"},
		{Address: "ws://localhost/ws", Token: " token "},
	} {
		if err := target.Validate(); err == nil {
			t.Fatalf("accepted invalid target: %q", target.Address)
		}
	}
}
