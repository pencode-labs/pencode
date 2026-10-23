package actions

import (
	"fmt"
	"sync"
	"testing"

	"github.com/pencode-labs/pencode/peb"
)

func TestRegistryLifecycleAndReentrantHandlers(t *testing.T) {
	var r Registry
	a := peb.Action{ID: "core/run", Text: "Run", Location: peb.TitleBarRight}
	clicks, wakes := 0, 0
	r.SetWake(func() { wakes++ })
	if err := r.Register(Definition{Action: a, Run: func(c peb.ActionContext) error {
		if c.ID != a.ID || c.Path != "/project/main.go" {
			t.Fatalf("wrong context: %+v", c)
		}
		clicks++
		return r.Update(peb.Action{ID: a.ID, Text: "Running", Location: peb.StatusBar})
	}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Definition{Action: a, Run: func(peb.ActionContext) error { return nil }}); err == nil {
		t.Fatal("duplicate accepted")
	}
	rows, revision := r.Snapshot(nil, 0)
	if len(rows) != 1 || rows[0] != a {
		t.Fatal(rows)
	}
	if allocs := testing.AllocsPerRun(100, func() { rows, revision = r.Snapshot(rows, revision) }); allocs != 0 {
		t.Fatalf("idle snapshot allocated %g", allocs)
	}
	if err := r.Run(peb.ActionContext{ID: a.ID, Path: "/project/main.go"}); err != nil {
		t.Fatal(err)
	}
	rows, revision = r.Snapshot(rows, revision)
	if clicks != 1 || wakes != 2 || rows[0].Text != "Running" || rows[0].Location != peb.StatusBar {
		t.Fatalf("state: %v, clicks=%d wakes=%d", rows, clicks, wakes)
	}
	updated := rows[0]
	updated.Disabled = true
	if err := r.Update(updated); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(peb.ActionContext{ID: a.ID}); err == nil {
		t.Fatal("disabled action ran")
	}
	updated.Disabled = false
	updated.Hidden = true
	_ = r.Update(updated)
	if err := r.Run(peb.ActionContext{ID: a.ID}); err == nil {
		t.Fatal("hidden action ran")
	}
	r.RemovePrefix("core/")
	rows, _ = r.Snapshot(rows, revision)
	if len(rows) != 0 {
		t.Fatal("owner removal retained buttons")
	}
}
func TestRegistryConcurrentUpdates(t *testing.T) {
	var r Registry
	var workers sync.WaitGroup
	for i := range 8 {
		workers.Go(func() {
			a := peb.Action{ID: fmt.Sprintf("worker/%d", i), Text: "Run"}
			if err := r.Register(Definition{Action: a, Run: func(peb.ActionContext) error { return nil }}); err != nil {
				t.Error(err)
			}
			for range 50 {
				a.Disabled = !a.Disabled
				if err := r.Update(a); err != nil {
					t.Error(err)
				}
			}
		})
	}
	workers.Wait()
	rows, _ := r.Snapshot(nil, 0)
	if len(rows) != 8 {
		t.Fatal(len(rows))
	}
}
