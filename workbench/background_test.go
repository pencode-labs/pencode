package workbench

import (
	"sync"
	"testing"
	"time"
)

func TestLatestWorkerCoalescesAndDoesNotBlockForeground(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	processed := make(chan int, 2)
	wakes := make(chan struct{}, 2)
	w := newLatestWorker(func(job int, result *int) bool {
		if job == 1 {
			close(entered)
			<-release
		}
		*result = job
		processed <- job
		return true
	}, func(result *int) { *result = 0 }, nil, func() { wakes <- struct{}{} })
	defer w.close()
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	w.submit(1)
	waitPencodeScanSignal(t, entered)
	for job := 2; job <= 100; job++ {
		w.submit(job)
	}
	if allocations := testing.AllocsPerRun(100, func() { w.submit(100) }); allocations != 0 {
		t.Fatalf("snapshot replacement allocated %g times", allocations)
	}
	unblock()
	for _, expected := range []int{1, 100} {
		select {
		case job := <-processed:
			if job != expected {
				t.Fatalf("processed obsolete snapshot %d, want %d", job, expected)
			}
		case <-time.After(time.Second):
			t.Fatal("latest snapshot did not run")
		}
		waitPencodeScanSignal(t, wakes)
	}
	var result int
	if !w.poll(func(value *int) { result = *value }) || result != 100 {
		t.Fatal("result mailbox did not retain only the newest result")
	}
	if allocations := testing.AllocsPerRun(100, func() { w.poll(func(*int) { t.Fatal("result was delivered twice") }) }); allocations != 0 {
		t.Fatalf("empty polling allocated %g times", allocations)
	}
}

func TestLatestWorkerCloseCancelsQueuedWorkAndDisposes(t *testing.T) {
	entered, release, disposed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	w := newLatestWorker(func(job int, result *int) bool {
		close(entered) // Executing the queued second task would panic.
		<-release
		return true
	}, func(*int) {}, func() { close(disposed) }, nil)
	w.submit(1)
	waitPencodeScanSignal(t, entered)
	w.submit(2)
	w.cancel()
	close(release)
	w.close()
	waitPencodeScanSignal(t, disposed)
	if w.pending || w.ready || w.request != 0 {
		t.Fatal("closed worker retained requests or results")
	}
}
