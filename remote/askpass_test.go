package remote

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if handled, err := RunAskpass(); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestSSHAskpassAuthenticatesHelperAndDoesNotCacheAnswers(t *testing.T) {
	var calls atomic.Int32
	ctx := WithPrompt(t.Context(), func(ctx context.Context, prompt AuthenticationPrompt) (string, error) {
		calls.Add(1)
		if prompt.Text != "user@host password:" || prompt.Confirm {
			return "", errors.New("unexpected prompt")
		}
		return "one-time-secret", nil
	})
	env, closePrompt, err := startAskpass(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer closePrompt()
	var address, token, executable string
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if strings.Contains(entry, "one-time-secret") {
			t.Fatal("password leaked into the environment")
		}
		switch key {
		case askpassAddress:
			address = value
		case askpassToken:
			token = value
		case "SSH_ASKPASS":
			executable = value
		}
	}
	if _, err := requestAskpass(address, "wrong-token", AuthenticationPrompt{Text: "user@host password:"}); err == nil || calls.Load() != 0 {
		t.Fatal("unauthorized helper invoked the prompt")
	}
	for range 2 {
		cmd := exec.CommandContext(t.Context(), executable, "user@host password:")
		cmd.Env = env
		output, err := cmd.Output()
		if err != nil || string(output) != "one-time-secret\n" {
			t.Fatal("askpass helper did not deliver the answer", err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("askpass reused a previous answer")
	}
	closePrompt()
	if _, err := requestAskpass(address, token, AuthenticationPrompt{Text: "user@host password:"}); err == nil {
		t.Fatal("authentication endpoint survived connection setup")
	}
}

func TestSSHAskpassCancellationUnblocksPrompt(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	entered := make(chan struct{})
	ctx = WithPrompt(ctx, func(ctx context.Context, _ AuthenticationPrompt) (string, error) {
		close(entered)
		<-ctx.Done()
		return "", ctx.Err()
	})
	env, closePrompt, err := startAskpass(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer closePrompt()
	var address, token string
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, askpassAddress+"="); ok {
			address = value
		}
		if value, ok := strings.CutPrefix(entry, askpassToken+"="); ok {
			token = value
		}
	}
	done := make(chan error, 1)
	go func() { _, err := requestAskpass(address, token, AuthenticationPrompt{Text: "password:"}); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("prompt did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled prompt supplied an answer")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not unblock the helper")
	}
}
