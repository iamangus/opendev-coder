//go:build integration

package gitops

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestExecGitOps_CloneAndWorktree(t *testing.T) {
	srcDir := t.TempDir()
	runCmd(t, srcDir, "git", "init")
	runCmd(t, srcDir, "git", "config", "user.email", "test@test.com")
	runCmd(t, srcDir, "git", "config", "user.name", "Test")
	writeFile(t, filepath.Join(srcDir, "hello.txt"), "hello")
	runCmd(t, srcDir, "git", "add", ".")
	runCmd(t, srcDir, "git", "commit", "-m", "initial")

	g := NewExec(slog.Default(), "")
	ctx := context.Background()

	cloneDir := filepath.Join(t.TempDir(), "clone")
	if err := g.Clone(ctx, srcDir, cloneDir); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	branch, err := g.DefaultBranch(ctx, cloneDir)
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if branch == "" {
		t.Fatal("DefaultBranch returned empty string")
	}

	if err := g.Fetch(ctx, cloneDir); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	wtDir := filepath.Join(t.TempDir(), "wt-feature")
	if err := g.CreateBranch(ctx, cloneDir, "feature", branch); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := g.WorktreeAdd(ctx, cloneDir, wtDir, "feature"); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}

	if _, err := os.Stat(filepath.Join(wtDir, "hello.txt")); err != nil {
		t.Fatalf("worktree missing hello.txt: %v", err)
	}

	diff, err := g.Diff(ctx, wtDir)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff != "" {
		t.Fatalf("expected empty diff, got: %s", diff)
	}

	status, err := g.Status(ctx, wtDir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != "" {
		t.Fatalf("expected clean status, got: %s", status)
	}

	if err := g.WorktreeRemove(ctx, cloneDir, wtDir); err != nil {
		t.Fatalf("WorktreeRemove: %v", err)
	}
	if _, err := os.Stat(wtDir); !os.IsNotExist(err) {
		t.Fatal("worktree directory should have been removed")
	}
}

func runCmd(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
