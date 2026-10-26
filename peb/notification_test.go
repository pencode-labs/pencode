package peb

import (
	"bytes"
	"context"
	"testing"
)

func TestNotificationCodecAndPublisher(t *testing.T) {
	n := Notification{ID: "tools", Title: "Go", Message: "安装失败\n详细日志", Action: "retry", ActionText: "重试", Error: true}
	data, err := EncodeNotification(nil, n)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeNotification(data)
	if err != nil || got != n {
		t.Fatal(got, err)
	}
	for i := range len(data) {
		if _, err := DecodeNotification(data[:i]); err == nil {
			t.Fatalf("accepted truncation at %d", i)
		}
	}
	if _, err := DecodeNotification(append(bytes.Clone(data), 0)); err == nil {
		t.Fatal("accepted trailing data")
	}
	for _, invalid := range []Notification{{}, {ID: "x"}, {ID: "x", Title: "x", Message: "x", Action: "retry"}} {
		if _, err := EncodeNotification(nil, invalid); err == nil {
			t.Fatal("accepted invalid message", invalid)
		}
	}
	if allocs := testing.AllocsPerRun(100, func() { data, _ = EncodeNotification(data, n) }); allocs != 0 {
		t.Fatalf("reused encoder allocated %g", allocs)
	}
	var stream bytes.Buffer
	ctx, cancel := context.WithCancel(t.Context())
	p := ActionPublisher{ctx: ctx, writer: &Writer{Target: &stream}}
	if err := p.Notify(n); err != nil {
		t.Fatal(err)
	}
	r := Reader{Source: &stream}
	f, err := r.Read()
	if err != nil || f.Kind != NotificationUpdate || f.ID != 0 {
		t.Fatal(f, err)
	}
	if validFrame(NotificationUpdate, 1, len(data)) {
		t.Fatal("accepted solicited notification")
	}
	cancel()
	if err := p.Notify(n); err != context.Canceled {
		t.Fatal("publisher ignored cancellation", err)
	}
}
