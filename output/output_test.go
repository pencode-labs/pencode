package output

import (
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestChannelKeepsBoundedTailAcrossWraps(t *testing.T) {
	var store Store
	c := store.Channel("test")
	want := ""
	for _, part := range []string{strings.Repeat("a", Capacity-3), "中文输出\n", strings.Repeat("b", 37), strings.Repeat("c", Capacity+11), "结束"} {
		_, _ = c.Write([]byte(part))
		want += part
		if len(want) > Capacity {
			want = want[len(want)-Capacity:]
		}
		for len(want) > 0 && !utf8.RuneStart(want[0]) {
			want = want[1:]
		}
		got, _ := c.Snapshot()
		if got != want || !utf8.ValidString(got) {
			t.Fatal("stream tail differs after wrap")
		}
		if len(c.data) != Capacity || c.used > Capacity {
			t.Fatal("unbounded output buffer")
		}
	}
	c.Clear()
	if got, _ := c.Snapshot(); got != "" {
		t.Fatalf("clear retained %q", got)
	}
	_, _ = c.Write([]byte("new"))
	if got, _ := c.Snapshot(); got != "new" {
		t.Fatalf("write after clear: %q", got)
	}
}

func TestOutputSourcesConcurrentAndIndependent(t *testing.T) {
	var store Store
	first, second := store.Channel("first"), store.Channel("second")
	if store.Channel("first") != first {
		t.Fatal("channel identity changed")
	}
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for range 100 {
				_, _ = first.Write([]byte("x\n"))
				first.Snapshot()
			}
		})
	}
	workers.Wait()
	_, _ = second.Write([]byte("other"))
	first.Clear()
	if got, _ := second.Snapshot(); got != "other" {
		t.Fatal("clear affected another source")
	}
	if channels := store.Channels(nil); len(channels) != 2 || channels[0] != first || channels[1] != second {
		t.Fatal("unstable source list")
	}
}

func TestOutputWarmWritesAndIdleSnapshotsDoNotAllocate(t *testing.T) {
	var store Store
	c := store.Channel("pencode")
	part := []byte("line\n")
	_, _ = c.Write(part)
	if n := testing.AllocsPerRun(100, func() { _, _ = c.Write(part) }); n != 0 {
		t.Fatalf("warm write allocated %g", n)
	}
	c.Snapshot()
	channels := store.Channels(nil)
	if n := testing.AllocsPerRun(100, func() { c.Snapshot(); store.Revision(); channels = store.Channels(channels) }); n != 0 {
		t.Fatalf("idle read allocated %g", n)
	}
}
