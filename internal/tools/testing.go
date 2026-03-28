package tools

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// TestRegistration holds a registered test command for a worktree.
type TestRegistration struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// ExecResult holds the result of running a test command.
type ExecResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	TimedOut bool   `json:"timed_out"`
}

// TestStore is a concurrency-safe, in-memory store mapping worktree root paths
// to their registered test commands. One registration per worktree; new
// registrations overwrite previous ones.
type TestStore struct {
	mu    sync.RWMutex
	tests map[string]TestRegistration
}

// NewTestStore creates an empty TestStore.
func NewTestStore() *TestStore {
	return &TestStore{tests: make(map[string]TestRegistration)}
}

// Register stores (or overwrites) the test command for the given worktree.
func (ts *TestStore) Register(worktreeRoot string, reg TestRegistration) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.tests[worktreeRoot] = reg
}

// Get returns the registration for a worktree, if any.
func (ts *TestStore) Get(worktreeRoot string) (TestRegistration, bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	reg, ok := ts.tests[worktreeRoot]
	return reg, ok
}

// Remove deletes the registration for a worktree.
func (ts *TestStore) Remove(worktreeRoot string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	delete(ts.tests, worktreeRoot)
}

// RegisterTest is the backing implementation for the register_test MCP tool.
// It stores the test command against the worktreeRoot and returns a
// confirmation message.
func RegisterTest(worktreeRoot, command, description string, store *TestStore) (string, error) {
	if command == "" {
		return "", fmt.Errorf("command is required")
	}
	store.Register(worktreeRoot, TestRegistration{
		Command:     command,
		Description: description,
	})
	return fmt.Sprintf("Test registered for worktree %s: %s", worktreeRoot, command), nil
}

// RunRegisteredTest looks up the registered test for the given worktree and
// executes it. Returns a structured ExecResult. If no test is registered,
// returns an error.
func RunRegisteredTest(worktreeRoot string, store *TestStore, timeout time.Duration, logger *slog.Logger) (*ExecResult, error) {
	reg, ok := store.Get(worktreeRoot)
	if !ok {
		return nil, fmt.Errorf("no test registered for worktree %q", worktreeRoot)
	}

	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	logger.Info("test run: executing command", "worktree", worktreeRoot, "command", reg.Command, "timeout_s", timeout.Seconds())

	stdout, stderr, exitCode, timedOut, err := ExecuteTerminalCommand(worktreeRoot, reg.Command, timeout)
	if err != nil {
		return nil, fmt.Errorf("executing test command: %w", err)
	}

	return &ExecResult{
		ExitCode: exitCode,
		Stdout:   stdout,
		Stderr:   stderr,
		TimedOut: timedOut,
	}, nil
}
