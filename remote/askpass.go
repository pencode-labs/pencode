package remote

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// AuthenticationPrompt distinguishes secrets from explicit yes/no decisions.
type AuthenticationPrompt struct {
	Text    string
	Confirm bool
}

// Prompt runs outside the UI thread and must return when ctx is canceled.
type Prompt func(ctx context.Context, prompt AuthenticationPrompt) (string, error)

type promptKey struct{}

func WithPrompt(ctx context.Context, prompt Prompt) context.Context {
	return context.WithValue(ctx, promptKey{}, prompt)
}

const askpassAddress = "PENCODE_SSH_ASKPASS_ADDRESS"
const askpassToken = "PENCODE_SSH_ASKPASS_TOKEN"

type askpassRequest struct {
	Token  string
	Prompt AuthenticationPrompt
}
type askpassResponse struct {
	Value    string
	Canceled bool
}

// RunAskpass handles OpenSSH's helper invocation before the GUI starts. Only
// this short-lived helper writes the answer to stdout, which SSH owns.
func RunAskpass() (bool, error) {
	address := os.Getenv(askpassAddress)
	if address == "" {
		return false, nil
	}
	prompt := "SSH 密码"
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}
	value, err := requestAskpass(address, os.Getenv(askpassToken), AuthenticationPrompt{
		Text: prompt, Confirm: os.Getenv("SSH_ASKPASS_PROMPT") == "confirm" || strings.HasPrefix(prompt, "The authenticity of host ") && strings.Contains(prompt, "Are you sure you want to continue connecting"),
	})
	if err == nil {
		_, err = fmt.Fprintln(os.Stdout, value)
	}
	return true, err
}

func requestAskpass(address, token string, prompt AuthenticationPrompt) (string, error) {
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))
	if err := json.NewEncoder(conn).Encode(askpassRequest{token, prompt}); err != nil {
		return "", err
	}
	var answer askpassResponse
	if err := json.NewDecoder(io.LimitReader(conn, 16<<10)).Decode(&answer); err != nil {
		return "", err
	}
	if answer.Canceled {
		return "", context.Canceled
	}
	return answer.Value, nil
}

func startAskpass(ctx context.Context) ([]string, func(), error) {
	prompt, _ := ctx.Value(promptKey{}).(Prompt)
	if prompt == nil {
		return nil, func() {}, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	token := rand.Text()
	serveCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	stop := context.AfterFunc(serveCtx, func() { _ = listener.Close() })
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer conn.Close()
				closeConn := context.AfterFunc(serveCtx, func() { _ = conn.Close() })
				defer closeConn()
				_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				var request askpassRequest
				if json.NewDecoder(io.LimitReader(conn, 16<<10)).Decode(&request) != nil ||
					subtle.ConstantTimeCompare([]byte(request.Token), []byte(token)) != 1 {
					return
				}
				value, err := prompt(serveCtx, request.Prompt)
				if errors.Is(err, context.Canceled) {
					cancel()
				}
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				_ = json.NewEncoder(conn).Encode(askpassResponse{Value: value, Canceled: err != nil})
			}()
		}
	}()
	env := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "SSH_ASKPASS", "SSH_ASKPASS_REQUIRE", "SSH_ASKPASS_PROMPT", askpassAddress, askpassToken:
		default:
			env = append(env, entry)
		}
	}
	env = append(env, "SSH_ASKPASS="+executable, "SSH_ASKPASS_REQUIRE=force", askpassAddress+"="+listener.Addr().String(), askpassToken+"="+token)
	return env, func() {
		cancel()
		stop()
		_ = listener.Close()
		<-done
	}, nil
}
