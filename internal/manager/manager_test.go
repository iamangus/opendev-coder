package manager_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iamangus/code-mcp/internal/manager"
)

// runGitExec runs a git command in dir and returns combined output.
func runGitExec(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// initGitRepo creates a minimal git repository with an initial commit so that
// git commands (symbolic-ref, worktree list, etc.) work correctly in tests.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	mustRun := func(args ...string) {
		t.Helper()
		out, err := runGitExec(dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustRun("init", "-b", "main")
	mustRun("config", "user.email", "test@example.com")
	mustRun("config", "user.name", "Test")
	// Create an initial commit so that symbolic-ref resolves correctly.
	readmePath := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readmePath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("writing README: %v", err)
	}
	mustRun("add", ".")
	mustRun("commit", "-m", "init")
}

// TestNew_CreatesMissingDir verifies that New creates the repos directory.
func TestNew_CreatesMissingDir(t *testing.T) {
	base := t.TempDir()
	reposDir := filepath.Join(base, "repos")

	mgr, err := manager.New(reposDir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if mgr.ReposDir() != reposDir {
		t.Errorf("ReposDir = %q, want %q", mgr.ReposDir(), reposDir)
	}
	if _, err := os.Stat(reposDir); os.IsNotExist(err) {
		t.Error("repos dir was not created")
	}
}

// TestRepoDir and TestBranchWorktreeDir verify path construction.
func TestRepoDir(t *testing.T) {
	mgr, _ := manager.New(t.TempDir())
	got := mgr.RepoDir("myrepo")
	if !strings.HasSuffix(got, "/myrepo") {
		t.Errorf("RepoDir = %q, want suffix /myrepo", got)
	}
}

func TestBranchWorktreeDir(t *testing.T) {
	mgr, _ := manager.New(t.TempDir())
	got := mgr.BranchWorktreeDir("myrepo", "feature")
	if !strings.HasSuffix(got, "/myrepo-feature") {
		t.Errorf("BranchWorktreeDir = %q, want suffix /myrepo-feature", got)
	}
}

// TestScan_EmptyDir verifies Scan returns nil for an empty repos dir.
func TestScan_EmptyDir(t *testing.T) {
	mgr, _ := manager.New(t.TempDir())
	repos, err := mgr.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(repos) != 0 {
		t.Errorf("expected 0 repos, got %d", len(repos))
	}
}

// TestScan_DiscoverMainClone verifies that a git repo placed in the repos dir
// is discovered by Scan.
func TestScan_DiscoverMainClone(t *testing.T) {
	reposDir := t.TempDir()
	repoDir := filepath.Join(reposDir, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repoDir)

	mgr, _ := manager.New(reposDir)
	repos, err := mgr.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if repos[0].Name != "myrepo" {
		t.Errorf("repo name = %q, want myrepo", repos[0].Name)
	}
	if repos[0].DefaultBranch == "" {
		t.Error("expected non-empty DefaultBranch")
	}
	if len(repos[0].Branches) == 0 {
		t.Error("expected at least one branch entry")
	}
}

// TestScan_IgnoresWorktreeDirs verifies that worktree directories (which have
// .git as a file, not a directory) are not reported as top-level repos.
func TestScan_IgnoresWorktreeDirs(t *testing.T) {
	reposDir := t.TempDir()

	// Create main repo.
	repoDir := filepath.Join(reposDir, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repoDir)

	// Create a branch and a worktree for it.
	if out, err := runGitExec(repoDir, "branch", "feature"); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}
	wtDir := filepath.Join(reposDir, "myrepo-feature")
	if out, err := runGitExec(repoDir, "worktree", "add", wtDir, "feature"); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	mgr, _ := manager.New(reposDir)
	repos, err := mgr.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	// Only myrepo (the main clone) should be returned.
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d: %v", len(repos), repos)
	}
	// The feature branch worktree should appear in the Branches list.
	var foundFeature bool
	for _, b := range repos[0].Branches {
		if b.Name == "feature" {
			foundFeature = true
		}
	}
	if !foundFeature {
		t.Errorf("feature branch not found in Branches: %v", repos[0].Branches)
	}
}

// TestRemoveRepo verifies that RemoveRepo removes the main clone and worktrees.
func TestRemoveRepo(t *testing.T) {
	reposDir := t.TempDir()
	repoDir := filepath.Join(reposDir, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repoDir)

	// Add a worktree directory (simulate one without git commands for speed).
	wtDir := filepath.Join(reposDir, "myrepo-feature")
	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		t.Fatal(err)
	}

	mgr, _ := manager.New(reposDir)
	if err := mgr.RemoveRepo("myrepo"); err != nil {
		t.Fatalf("RemoveRepo: %v", err)
	}

	if _, err := os.Stat(repoDir); !os.IsNotExist(err) {
		t.Error("main repo dir still exists after RemoveRepo")
	}
	if _, err := os.Stat(wtDir); !os.IsNotExist(err) {
		t.Error("worktree dir still exists after RemoveRepo")
	}
}

// TestRemoveRepo_NotFound verifies that removing a non-existent repo returns error.
func TestRemoveRepo_NotFound(t *testing.T) {
	mgr, _ := manager.New(t.TempDir())
	if err := mgr.RemoveRepo("nonexistent"); err == nil {
		t.Error("expected error for non-existent repo")
	}
}

// TestCreateAndRemoveWorktree tests the full worktree lifecycle.
func TestCreateAndRemoveWorktree(t *testing.T) {
	reposDir := t.TempDir()
	repoDir := filepath.Join(reposDir, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repoDir)

	// Create a branch to check out as a worktree.
	if out, err := runGitExec(repoDir, "branch", "feature"); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}

	mgr, _ := manager.New(reposDir)
	if err := mgr.CreateWorktree("myrepo", "feature", ""); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	wtDir := filepath.Join(reposDir, "myrepo-feature")
	if _, err := os.Stat(wtDir); os.IsNotExist(err) {
		t.Error("worktree dir was not created")
	}

	// Verify worktree is found by WorktreeDir.
	got, err := mgr.WorktreeDir("myrepo", "feature")
	if err != nil {
		t.Fatalf("WorktreeDir: %v", err)
	}
	if got != wtDir {
		t.Errorf("WorktreeDir = %q, want %q", got, wtDir)
	}

	// Remove the worktree.
	if err := mgr.RemoveWorktree("myrepo", "feature"); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wtDir); !os.IsNotExist(err) {
		t.Error("worktree dir still exists after RemoveWorktree")
	}
}

// TestCreateWorktree_InvalidBranch verifies that branch names with slashes are rejected.
func TestCreateWorktree_InvalidBranch(t *testing.T) {
	reposDir := t.TempDir()
	repoDir := filepath.Join(reposDir, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repoDir)

	mgr, _ := manager.New(reposDir)
	if err := mgr.CreateWorktree("myrepo", "feature/sub", ""); err == nil {
		t.Error("expected error for branch name with slash")
	}
}

// TestCreateWorktree_AlreadyExists verifies duplicate creation returns error.
func TestCreateWorktree_AlreadyExists(t *testing.T) {
	reposDir := t.TempDir()
	repoDir := filepath.Join(reposDir, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repoDir)

	if out, err := runGitExec(repoDir, "branch", "feature"); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}

	mgr, _ := manager.New(reposDir)
	if err := mgr.CreateWorktree("myrepo", "feature", ""); err != nil {
		t.Fatalf("first CreateWorktree: %v", err)
	}
	if err := mgr.CreateWorktree("myrepo", "feature", ""); err == nil {
		t.Error("expected error on duplicate CreateWorktree")
	}
}

// TestCreateWorktree_WithBase verifies that a new branch is forked from the
// specified base branch rather than HEAD.
func TestCreateWorktree_WithBase(t *testing.T) {
	reposDir := t.TempDir()
	repoDir := filepath.Join(reposDir, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repoDir)

	// Create a base branch with a unique commit so we can verify the feature
	// branch was forked from it.
	mustRun := func(args ...string) {
		t.Helper()
		out, err := runGitExec(repoDir, args...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustRun("checkout", "-b", "orch-base")
	if err := os.WriteFile(filepath.Join(repoDir, "orch.txt"), []byte("orch"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun("add", ".")
	mustRun("commit", "-m", "orch commit")

	mgr, _ := manager.New(reposDir)
	if err := mgr.CreateWorktree("myrepo", "feature-child", "orch-base"); err != nil {
		t.Fatalf("CreateWorktree with base: %v", err)
	}

	wtDir := filepath.Join(reposDir, "myrepo-feature-child")
	if _, err := os.Stat(wtDir); os.IsNotExist(err) {
		t.Fatal("worktree dir was not created")
	}

	// The orch.txt file should be present in the feature branch, proving it was
	// forked from orch-base.
	if _, err := os.Stat(filepath.Join(wtDir, "orch.txt")); os.IsNotExist(err) {
		t.Error("orch.txt not found in feature worktree — branch was not forked from orch-base")
	}
}

// TestMergeBranch_Success verifies a clean merge between two branches.
func TestMergeBranch_Success(t *testing.T) {
	reposDir := t.TempDir()
	repoDir := filepath.Join(reposDir, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repoDir)

	mustRun := func(args ...string) {
		t.Helper()
		out, err := runGitExec(repoDir, args...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Create orchestration branch off main.
	mustRun("checkout", "-b", "orch-test")
	// Create feature branch off orch-test with a unique file.
	mustRun("checkout", "-b", "feature-test")
	if err := os.WriteFile(filepath.Join(repoDir, "feature.txt"), []byte("feature work"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun("add", ".")
	mustRun("commit", "-m", "feature work")

	// Go back to orch-test to set up worktrees (repo HEAD must not be on these branches).
	mustRun("checkout", "main")

	mgr, _ := manager.New(reposDir)

	// Create worktrees for both branches.
	if err := mgr.CreateWorktree("myrepo", "orch-test", ""); err != nil {
		t.Fatalf("CreateWorktree orch-test: %v", err)
	}
	if err := mgr.CreateWorktree("myrepo", "feature-test", ""); err != nil {
		t.Fatalf("CreateWorktree feature-test: %v", err)
	}

	// Merge feature-test into orch-test.
	if err := mgr.MergeBranch("myrepo", "feature-test", "orch-test"); err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}

	// Verify the file landed in the orch-test worktree.
	orchWTDir := filepath.Join(reposDir, "myrepo-orch-test")
	if _, err := os.Stat(filepath.Join(orchWTDir, "feature.txt")); os.IsNotExist(err) {
		t.Error("feature.txt not found in orch-test after merge")
	}
}

// TestMergeBranch_Conflict verifies that a conflict returns a MergeConflictError.
func TestMergeBranch_Conflict(t *testing.T) {
	reposDir := t.TempDir()
	repoDir := filepath.Join(reposDir, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repoDir)

	mustRun := func(dir string, args ...string) {
		t.Helper()
		out, err := runGitExec(dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Create orch branch with a file.
	mustRun(repoDir, "checkout", "-b", "orch-conflict")
	if err := os.WriteFile(filepath.Join(repoDir, "conflict.txt"), []byte("orch version"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(repoDir, "add", ".")
	mustRun(repoDir, "commit", "-m", "orch version")

	// Create feature branch with a conflicting change to the same file.
	mustRun(repoDir, "checkout", "-b", "feature-conflict", "main")
	if err := os.WriteFile(filepath.Join(repoDir, "conflict.txt"), []byte("feature version"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(repoDir, "add", ".")
	mustRun(repoDir, "commit", "-m", "feature version")

	mustRun(repoDir, "checkout", "main")

	mgr, _ := manager.New(reposDir)
	if err := mgr.CreateWorktree("myrepo", "orch-conflict", ""); err != nil {
		t.Fatalf("CreateWorktree orch-conflict: %v", err)
	}
	if err := mgr.CreateWorktree("myrepo", "feature-conflict", ""); err != nil {
		t.Fatalf("CreateWorktree feature-conflict: %v", err)
	}

	err := mgr.MergeBranch("myrepo", "feature-conflict", "orch-conflict")
	if err == nil {
		t.Fatal("expected merge conflict error, got nil")
	}
	var conflictErr *manager.MergeConflictError
	if !isConflictError(err, &conflictErr) {
		t.Fatalf("expected *MergeConflictError, got %T: %v", err, err)
	}
	if conflictErr.Output == "" {
		t.Error("expected non-empty conflict output")
	}

	// Verify the worktree is left in a clean state (merge was aborted).
	orchWTDir := filepath.Join(reposDir, "myrepo-orch-conflict")
	out, err2 := runGitExec(orchWTDir, "status", "--porcelain")
	if err2 != nil {
		t.Fatalf("git status: %v", err2)
	}
	if strings.Contains(out, "UU") || strings.Contains(out, "AA") {
		t.Errorf("worktree has unresolved conflicts after abort: %s", out)
	}
}

// isConflictError is a helper to avoid importing errors package just for As.
func isConflictError(err error, target **manager.MergeConflictError) bool {
	ce, ok := err.(*manager.MergeConflictError)
	if ok {
		*target = ce
	}
	return ok
}

// TestWorktreeDir_DefaultBranch verifies that WorktreeDir returns the main repo
// dir for the default branch.
func TestWorktreeDir_DefaultBranch(t *testing.T) {
	reposDir := t.TempDir()
	repoDir := filepath.Join(reposDir, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repoDir)

	mgr, _ := manager.New(reposDir)
	got, err := mgr.WorktreeDir("myrepo", "main")
	if err != nil {
		t.Fatalf("WorktreeDir: %v", err)
	}
	if got != repoDir {
		t.Errorf("WorktreeDir = %q, want %q", got, repoDir)
	}
}
