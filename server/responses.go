package server

import (
	"context"
	"io"
	"sync"

	"github.com/diiyw/onix/projects/pencode/protocol"
	"github.com/diiyw/onix/projects/pencode/server/workspace"
)

const (
	maxResponses     = 64
	maxResponseBytes = 128 << 20
)

type response struct {
	id     uint64
	kind   protocol.Kind
	result *protocol.Encoder
	offset int
}

// responses owns queued buffers until their last frame is written. Producers
// never write to the transport; a slow download cannot hold the request reader.
// The byte/count budgets apply to queued and currently transmitting responses.
type responses struct {
	ctx   context.Context
	queue chan response
	pool  sync.Pool
	mu    sync.Mutex
	space *sync.Cond
	count int
	bytes int
	live  map[uint64]bool // true means cancelled, only queued responses are kept
}

func newResponses(ctx context.Context) *responses {
	r := &responses{ctx: ctx, queue: make(chan response, maxResponses), live: make(map[uint64]bool)}
	r.space = sync.NewCond(&r.mu)
	r.pool.New = func() any { return new(protocol.Encoder) }
	context.AfterFunc(ctx, func() {
		r.mu.Lock()
		r.space.Broadcast()
		r.mu.Unlock()
	})
	return r
}

func (r *responses) encoder() *protocol.Encoder {
	e := r.pool.Get().(*protocol.Encoder)
	e.Reset()
	return e
}

func (r *responses) recycle(e *protocol.Encoder) {
	if cap(e.Buffer) > 1<<20 {
		e.Buffer = nil
	}
	r.pool.Put(e)
}

func (r *responses) reply(id uint64, e *protocol.Encoder, err error) {
	kind := protocol.Reply
	if len(e.Buffer) > protocol.MaxMessage {
		err = &protocol.Error{Code: protocol.Busy, Message: "service response exceeds budget"}
	}
	if err != nil {
		if cap(e.Buffer) > 1<<20 {
			e.Buffer = nil
		}
		e.Reset()
		e.U32(uint32(workspace.ErrorCode(err)))
		e.String(err.Error())
		kind = protocol.Failure
	}
	r.mu.Lock()
	for r.ctx.Err() == nil && (r.count == maxResponses || r.bytes+cap(e.Buffer) > maxResponseBytes) {
		r.space.Wait()
	}
	if r.ctx.Err() != nil {
		r.mu.Unlock()
		r.recycle(e)
		return
	}
	r.count++
	r.bytes += cap(e.Buffer)
	r.live[id] = false
	r.mu.Unlock()
	select {
	case r.queue <- response{id: id, kind: kind, result: e}:
	case <-r.ctx.Done():
		r.release(id, e)
	}
}

func (r *responses) cancel(id uint64) {
	r.mu.Lock()
	if _, ok := r.live[id]; ok {
		r.live[id] = true
	}
	r.mu.Unlock()
}

func (r *responses) release(id uint64, e *protocol.Encoder) {
	r.mu.Lock()
	r.count--
	r.bytes -= cap(e.Buffer)
	delete(r.live, id)
	r.space.Broadcast()
	r.mu.Unlock()
	r.recycle(e)
}

func (r *responses) run(target io.Writer) error {
	w := protocol.Writer{Target: target}
	var pending [maxResponses]response
	head, count := 0, 0
	preferNew := true
	for {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		var next response
		if count > 0 {
			if preferNew && count < len(pending) {
				select {
				case next = <-r.queue:
				default:
				}
			}
			preferNew = !preferNew
			if next.result == nil {
				next, pending[head] = pending[head], response{}
				head = (head + 1) % len(pending)
				count--
			}
		} else {
			select {
			case <-r.ctx.Done():
				return r.ctx.Err()
			case next = <-r.queue:
			}
		}
		r.mu.Lock()
		cancelled := r.live[next.id]
		r.mu.Unlock()
		if cancelled {
			r.release(next.id, next.result)
			continue
		}
		payload, kind := next.result.Buffer[next.offset:], next.kind
		if len(payload) > protocol.MaxPayload {
			payload, kind = payload[:protocol.MaxPayload], protocol.Data
		}
		if err := w.Write(kind, next.id, payload); err != nil {
			return err
		}
		if kind == protocol.Data {
			next.offset += len(payload)
			pending[(head+count)%len(pending)] = next
			count++
		} else {
			r.release(next.id, next.result)
		}
	}
}
