package gitops

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

type Exec struct {
	logger *slog.Logger
	token  string
}

func NewExec(logger *slog.Logger, token string) *Exec {
	return &Exec{logger: logger, token: token}
}

func (e *Exec) Clone(ctx context.Context, url, dir string) error {
	_, err := e.run(ctx, "", "git", "clone", "--bare", e.authURL(url), dir)
	return err
}

func (e *Exec) Fetch(ctx context.Context, dir string) error {
	_, err := e.run(ctx, dir, "git", "fetch", "--prune", "origin")
	return err
}

func (e *Exec) WorktreeAdd(ctx context.Context, repoDir, wtDir, branch string) error {
	_, err := e.run(ctx, repoDir, "git", "worktree", "add", wtDir, branch)
	return err
}

func (e *Exec) WorktreeRemove(ctx context.Context, repoDir, wtDir string) error {
	_, err := e.run(ctx, repoDir, "git", "worktree", "remove", "--force", wtDir)
	return err
}

func (e *Exec) Merge(ctx context.Context, dir, branch string) error {
	_, err := e.run(ctx, dir, "git", "merge", branch)
	return err
}

func (e *Exec) Push(ctx context.Context, dir, branch string) error {
	_, err := e.run(ctx, dir, "git", "push", "origin", branch)
	return err
}

func (e *Exec) Diff(ctx context.Context, dir string) (string, error) {
	return e.run(ctx, dir, "git", "diff", "HEAD")
}

func (e *Exec) CommitLog(ctx context.Context, dir string, args ...string) (string, error) {
	fullArgs := append([]string{"log"}, args...)
	return e.run(ctx, dir, "git", fullArgs...)
}

func (e *Exec) DefaultBranch(ctx context.Context, dir string) (string, error) {
	out, err := e.run(ctx, dir, "git", "symbolic-ref", "--short", "HEAD")
	if err != nil {
		out, err = e.run(ctx, dir, "git", "rev-parse", "--abbrev-ref", "origin/HEAD")
		if err != nil {
			return "", err
		}
		out = strings.TrimPrefix(out, "origin/")
	}
	return strings.TrimSpace(out), nil
}

func (e *Exec) BranchExists(ctx context.Context, dir, branch string) (bool, error) {
	_, err := e.run(ctx, dir, "git", "rev-parse", "--verify", branch)
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (e *Exec) RemoteBranchExists(ctx context.Context, dir, branch string) (bool, error) {
	_, err := e.run(ctx, dir, "git", "rev-parse", "--verify", "origin/"+branch)
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (e *Exec) CreateBranch(ctx context.Context, dir, branch, startPoint string) error {
	_, err := e.run(ctx, dir, "git", "branch", branch, startPoint)
	return err
}

func (e *Exec) Status(ctx context.Context, dir string) (string, error) {
	return e.run(ctx, dir, "git", "status", "--short")
}

func (e *Exec) authURL(rawURL string) string {
	if e.token == "" {
		return rawURL
	}
	if strings.HasPrefix(rawURL, "https://") {
		return strings.Replace(rawURL, "https://", "https://x-access-token:"+e.token+"@", 1)
	}
	return rawURL
}

func (e *Exec) run(ctx context.Context, dir, name string, args ...string) (string, error) {
	start := time.Now()
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}

	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)
	result := strings.TrimSpace(string(out))

	if err != nil {
		e.logger.Error("git command failed",
			"cmd", name,
			"args", args,
			"dir", dir,
			"duration_ms", elapsed.Milliseconds(),
			"error", err.Error(),
			"output", result,
		)
		return result, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, result)
	}

	e.logger.Debug("git command completed",
		"cmd", name,
		"args", args,
		"dir", dir,
		"duration_ms", elapsed.Milliseconds(),
	)
	return result, nil
}

// Compile-time check that Exec implements GitOps.
var _ GitOps = (*Exec)(nil)
