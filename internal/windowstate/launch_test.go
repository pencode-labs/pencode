package windowstate

import (
	"slices"
	"testing"
	"time"
)

func TestForwardLaunchWakesOwnerAndHandsOffAfterClose(t *testing.T) {
	dir := t.TempDir()
	woken := make(chan struct{}, 1)
	wake := func() {
		select {
		case woken <- struct{}{}:
		default:
		}
	}
	owner, err := ForwardLaunch(dir, "first", wake)
	if err != nil || owner == nil {
		t.Fatal("claim desktop", err)
	}
	defer owner.Close()
	var received []string
	receive := func(value string) { received = append(received, value) }
	if err := owner.Drain(false, receive); err != nil {
		t.Fatal(err)
	}
	if next, err := ForwardLaunch(dir, "second", wake); next != nil || err != nil {
		t.Fatal("forward", err)
	}
	select {
	case <-woken:
	case <-time.After(5 * time.Second):
		t.Fatal("idle desktop did not wake")
	}
	if err := owner.Drain(true, receive); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(received, []string{"second"}) {
		t.Fatalf("received %q", received)
	}
	if next, err := ForwardLaunch(dir, "third", wake); next != nil || err != nil {
		t.Fatal("released desktop with a pending window", err)
	}
	if err := owner.Drain(true, receive); err != nil {
		t.Fatal(err)
	}
	if err := owner.Drain(true, receive); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(received, []string{"second", "third"}) {
		t.Fatalf("received %q", received)
	}
	next, err := ForwardLaunch(dir, "fresh", wake)
	if err != nil || next == nil {
		t.Fatal("last close kept the lease", err)
	}
	next.Close()
}
