package server

import (
	"context"
	"testing"
	"time"
)

func TestResponseBackpressureCancelledOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := newResponses(ctx)
	for id := 1; id <= maxResponses; id++ {
		e := r.encoder()
		e.U64(uint64(id))
		r.reply(uint64(id), e, nil)
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		r.reply(maxResponses+1, r.encoder(), nil)
	}()
	select {
	case <-finished:
		t.Fatal("slow peer bypassed the response count budget")
	case <-time.After(10 * time.Millisecond):
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not release a backpressured producer")
	}
}
