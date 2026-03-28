package locks

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestLM() *Manager {
	return NewManager(slog.Default())
}

func TestManager_BasicReadWrite(t *testing.T) {
	lm := newTestLM()
	ctx := context.Background()

	if err := lm.RLock(ctx, "/a"); err != nil {
		t.Fatalf("RLock: %v", err)
	}
	lm.RUnlock("/a")

	if err := lm.Lock(ctx, "/a"); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	lm.Unlock("/a")
}

func TestManager_MultipleReaders(t *testing.T) {
	lm := newTestLM()
	ctx := context.Background()
	var active atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := lm.RLock(ctx, "/b"); err != nil {
				t.Errorf("RLock: %v", err)
				return
			}
			active.Add(1)
			time.Sleep(10 * time.Millisecond)
			active.Add(-1)
			lm.RUnlock("/b")
		}()
	}
	wg.Wait()
}

func TestManager_ContextCancellation(t *testing.T) {
	lm := newTestLM()
	ctx := context.Background()

	if err := lm.Lock(ctx, "/c"); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()

	err := lm.RLock(cancelCtx, "/c")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}

	lm.Unlock("/c")
}

func TestManager_ContextTimeout(t *testing.T) {
	lm := newTestLM()
	ctx := context.Background()

	if err := lm.Lock(ctx, "/d"); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	err := lm.Lock(timeoutCtx, "/d")
	if err == nil {
		t.Fatal("expected error from timeout")
	}

	lm.Unlock("/d")
}

func TestManager_ReferenceCountCleanup(t *testing.T) {
	lm := newTestLM()
	ctx := context.Background()

	if err := lm.Lock(ctx, "/e"); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	lm.Unlock("/e")

	lm.mu.Lock()
	_, exists := lm.locks["/e"]
	lm.mu.Unlock()

	if exists {
		t.Fatal("expected lock entry to be cleaned up after last unlock")
	}
}

func TestManager_SeparatePaths(t *testing.T) {
	lm := newTestLM()
	ctx := context.Background()

	if err := lm.Lock(ctx, "/f"); err != nil {
		t.Fatalf("Lock /f: %v", err)
	}
	if err := lm.RLock(ctx, "/g"); err != nil {
		t.Fatalf("RLock /g: %v", err)
	}
	lm.RUnlock("/g")
	lm.Unlock("/f")
}

func TestManager_WriteExcludesReaders(t *testing.T) {
	lm := newTestLM()
	ctx := context.Background()

	if err := lm.Lock(ctx, "/h"); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	done := make(chan struct{})
	go func() {
		if err := lm.RLock(ctx, "/h"); err != nil {
			t.Errorf("RLock: %v", err)
			close(done)
			return
		}
		lm.RUnlock("/h")
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("reader should have been blocked by writer")
	default:
	}

	lm.Unlock("/h")
	<-done
}
