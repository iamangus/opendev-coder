package locks

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const DefaultWarnAfter = 100 * time.Millisecond

type lockEntry struct {
	rwmu sync.RWMutex
	refs int
}

type Manager struct {
	mu        sync.Mutex
	locks     map[string]*lockEntry
	logger    *slog.Logger
	warnAfter time.Duration
}

type Option func(*Manager)

func WithWarnAfter(d time.Duration) Option {
	return func(m *Manager) { m.warnAfter = d }
}

func NewManager(logger *slog.Logger, opts ...Option) *Manager {
	m := &Manager{
		locks:     make(map[string]*lockEntry),
		logger:    logger,
		warnAfter: DefaultWarnAfter,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

func (m *Manager) getOrCreate(path string) *lockEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.locks[path]
	if !ok {
		e = &lockEntry{}
		m.locks[path] = e
	}
	e.refs++
	return e
}

func (m *Manager) release(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.locks[path]
	if !ok {
		return
	}
	e.refs--
	if e.refs <= 0 {
		delete(m.locks, path)
	}
}

func (m *Manager) RLock(ctx context.Context, path string) error {
	return m.acquireLock(ctx, path, false)
}

func (m *Manager) RUnlock(path string) {
	m.mu.Lock()
	e, ok := m.locks[path]
	m.mu.Unlock()
	if ok {
		e.rwmu.RUnlock()
	}
	m.release(path)
	m.logger.Debug("lock released", "path", path, "mode", "read")
}

func (m *Manager) Lock(ctx context.Context, path string) error {
	return m.acquireLock(ctx, path, true)
}

func (m *Manager) Unlock(path string) {
	m.mu.Lock()
	e, ok := m.locks[path]
	m.mu.Unlock()
	if ok {
		e.rwmu.Unlock()
	}
	m.release(path)
	m.logger.Debug("lock released", "path", path, "mode", "write")
}

func (m *Manager) acquireLock(ctx context.Context, path string, exclusive bool) error {
	e := m.getOrCreate(path)
	start := time.Now()

	acquired := make(chan struct{})
	go func() {
		if exclusive {
			e.rwmu.Lock()
		} else {
			e.rwmu.RLock()
		}
		close(acquired)
	}()

	mode := "read"
	if exclusive {
		mode = "write"
	}

	select {
	case <-acquired:
		elapsed := time.Since(start)
		if elapsed >= m.warnAfter {
			m.logger.Warn("lock contention", "path", path, "mode", mode, "wait_ms", elapsed.Milliseconds())
		} else {
			m.logger.Debug("lock acquired", "path", path, "mode", mode, "wait_ms", elapsed.Milliseconds())
		}
		return nil
	case <-ctx.Done():
		go func() {
			<-acquired
			if exclusive {
				e.rwmu.Unlock()
			} else {
				e.rwmu.RUnlock()
			}
			m.release(path)
		}()
		return ctx.Err()
	}
}
