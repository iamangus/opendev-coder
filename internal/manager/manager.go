// Package manager handles discovery, cloning, and worktree management for
// multiple Git repositories stored under a single root directory.
//
// Directory layout
//
//	/repos/<name>           ← main clone (default branch)
//	/repos/<name>-<branch>  ← git worktree for <branch>
package manager

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// RepoInfo describes a discovered repository and all of its worktrees.
type RepoInfo struct {
	Name          string       `json:"name"`
	Dir           string       `json:"dir"`
	DefaultBranch string       `json:"default_branch"`
	Branches      []BranchInfo `json:"branches"`
}

// BranchInfo describes one branch (main clone or worktree).
type BranchInfo struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`
}

// Manager manages repositories and their worktrees under ReposDir.
type Manager struct {
	reposDir string
	token    string // optional GitHub token used to authenticate all git operations
	mu       sync.RWMutex
}

// New returns a Manager rooted at reposDir, creating the directory if needed.
// token is an optional GitHub personal-access token. When non-empty it is
// injected into every git command via the GIT_CONFIG_* env vars so that all
// push/fetch/clone operations against https://github.com/ authenticate
// automatically without prompting.
func New(reposDir, token string) (*Manager, error) {
	if err := os.MkdirAll(reposDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating repos dir %q: %w", reposDir, err)
	}
	return &Manager{reposDir: reposDir, token: token}, nil
}

// ReposDir returns the root directory where repositories are stored.
func (m *Manager) ReposDir() string { return m.reposDir }

// RepoDir returns the filesystem path for the main clone of a repository.
func (m *Manager) RepoDir(repo string) string {
	return filepath.Join(m.reposDir, repo)
}

// BranchWorktreeDir returns the filesystem path for a branch worktree.
// The main (default) branch lives in RepoDir, not here.
func (m *Manager) BranchWorktreeDir(repo, branch string) string {
	return filepath.Join(m.reposDir, repo+"-"+branch)
}

// WorktreeDir returns the filesystem path for a repo/branch pair.
// For the default branch it returns the main repo directory; for other
// branches it returns the worktree directory.
func (m *Manager) WorktreeDir(repo, branch string) (string, error) {
	repoDir := filepath.Join(m.reposDir, repo)
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		return "", fmt.Errorf("repo %q not found", repo)
	}

	defaultBranch, _ := getDefaultBranch(repoDir)
	if branch == defaultBranch {
		return repoDir, nil
	}

	wt := filepath.Join(m.reposDir, repo+"-"+branch)
	if _, err := os.Stat(wt); err == nil {
		return wt, nil
	}

	return "", fmt.Errorf("branch %q not found for repo %q", branch, repo)
}

// Scan discovers all main clones and their worktrees in the repos directory.
func (m *Manager) Scan() ([]RepoInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.scan()
}

func (m *Manager) scan() ([]RepoInfo, error) {
	entries, err := os.ReadDir(m.reposDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading repos dir: %w", err)
	}

	var repos []RepoInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(m.reposDir, e.Name())
		gitPath := filepath.Join(dir, ".git")
		info, statErr := os.Stat(gitPath)
		if statErr != nil {
			continue
		}
		// Main repos have .git as a directory; worktrees have .git as a file.
		if !info.IsDir() {
			continue
		}

		repoName := e.Name()
		defaultBranch, _ := getDefaultBranch(dir)
		if defaultBranch == "" {
			defaultBranch = "main"
		}

		branches, wtErr := listWorktrees(dir, repoName, defaultBranch)
		if wtErr != nil || len(branches) == 0 {
			branches = []BranchInfo{{Name: defaultBranch, Dir: dir}}
		}

		repos = append(repos, RepoInfo{
			Name:          repoName,
			Dir:           dir,
			DefaultBranch: defaultBranch,
			Branches:      branches,
		})
	}
	return repos, nil
}

// CloneRepo clones repoURL into <reposDir>/<name>.
// token is an optional HTTPS auth token (embedded in the URL).
func (m *Manager) CloneRepo(repoURL, name, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate name: must be a simple directory component
	if name == "" || strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("invalid repo name %q", name)
	}

	destDir := filepath.Join(m.reposDir, name)
	if _, err := os.Stat(destDir); err == nil {
		return fmt.Errorf("repo %q already exists", name)
	}

	cloneURL := repoURL
	if token != "" {
		cloneURL = embedToken(repoURL, token)
	}

	if out, err := m.runCmd("", "git", "clone", cloneURL, destDir); err != nil {
		return fmt.Errorf("git clone: %w\n%s", err, out)
	}
	return nil
}

// RemoveRepo removes the main clone and all associated worktree directories.
func (m *Manager) RemoveRepo(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	repoDir := filepath.Join(m.reposDir, name)
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		return fmt.Errorf("repo %q not found", name)
	}

	// Remove worktree directories first.
	prefix := name + "-"
	entries, _ := os.ReadDir(m.reposDir)
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			_ = os.RemoveAll(filepath.Join(m.reposDir, e.Name()))
		}
	}

	return os.RemoveAll(repoDir)
}

// CreateWorktree creates a git worktree at <reposDir>/<repo>-<branch> for the
// given branch. It fetches the remote first so that newly pushed branches are
// available without requiring a separate fetch step.
//
// base specifies the commit-ish to branch from when creating a brand-new
// branch. If base is empty, HEAD of the main clone is used. base is ignored
// when the branch already exists locally or at origin.
func (m *Manager) CreateWorktree(repo, branch, base string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate branch: must be a simple component (no path separators)
	if branch == "" || strings.ContainsAny(branch, "/\\") {
		return fmt.Errorf("invalid branch name %q (use '-' instead of '/' in the URL)", branch)
	}

	repoDir := filepath.Join(m.reposDir, repo)
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		return fmt.Errorf("repo %q not found", repo)
	}

	worktreeDir := filepath.Join(m.reposDir, repo+"-"+branch)
	if _, err := os.Stat(worktreeDir); err == nil {
		return fmt.Errorf("worktree for branch %q already exists", branch)
	}

	// Fetch so that newly pushed branches are discoverable.
	// Log but don't fail on fetch errors – the branch may still exist locally.
	if out, fetchErr := m.runCmd(repoDir, "git", "fetch", "--quiet", "origin"); fetchErr != nil {
		log.Printf("manager: git fetch in %q (non-fatal): %v\n%s", repoDir, fetchErr, out)
	}

	// Check whether the branch already exists (locally or as a remote-tracking ref).
	// If it does, check it out directly; otherwise create a new branch off base (or HEAD).
	_, refErr := m.runCmd(repoDir, "git", "rev-parse", "--verify", branch)
	if refErr != nil {
		// Also check for a remote-tracking ref (origin/<branch>).
		_, remoteRefErr := m.runCmd(repoDir, "git", "rev-parse", "--verify", "origin/"+branch)
		if remoteRefErr != nil {
			// Branch does not exist anywhere — create it off base (fallback: HEAD).
			startPoint := base
			if startPoint == "" {
				startPoint = "HEAD"
			}
			if out, err := m.runCmd(repoDir, "git", "worktree", "add", "-b", branch, worktreeDir, startPoint); err != nil {
				return fmt.Errorf("git worktree add: %w\n%s", err, out)
			}
			// Push the new branch to origin so GitHub knows about it.
			if out, pushErr := m.runCmd(repoDir, "git", "push", "-u", "origin", branch); pushErr != nil {
				log.Printf("manager: git push origin %s (non-fatal): %v\n%s", branch, pushErr, out)
			}
			return nil
		}
	}

	// Branch exists locally or at origin — check it out into the worktree.
	if out, err := m.runCmd(repoDir, "git", "worktree", "add", worktreeDir, branch); err != nil {
		return fmt.Errorf("git worktree add: %w\n%s", err, out)
	}
	return nil
}

// PushBranch pushes the given branch to the origin remote.
// It runs from the main repo directory (not a worktree). Non-fatal errors
// (e.g. when the remote does not accept the push) are returned to the caller.
func (m *Manager) PushBranch(repo, branch string) error {
	repoDir := filepath.Join(m.reposDir, repo)
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		return fmt.Errorf("repo %q not found", repo)
	}
	if out, err := m.runCmd(repoDir, "git", "push", "-u", "origin", branch); err != nil {
		return fmt.Errorf("git push origin %s: %w\n%s", branch, err, out)
	}
	return nil
}

// MergeConflictError is returned by MergeBranch when the merge cannot be
// completed automatically due to conflicts. The output from git is captured
// in the Output field so callers can surface it to an agent for resolution.
type MergeConflictError struct {
	Output string
}

func (e *MergeConflictError) Error() string {
	return "merge conflict: " + e.Output
}

// MergeBranch merges sourceBranch into targetBranch using --no-ff inside the
// target branch's worktree. It returns a *MergeConflictError if git exits
// non-zero (conflict or other merge failure), so callers can distinguish
// conflicts from other errors.
func (m *Manager) MergeBranch(repo, sourceBranch, targetBranch string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	targetDir, err := m.worktreeDirLocked(repo, targetBranch)
	if err != nil {
		return fmt.Errorf("target branch %q: %w", targetBranch, err)
	}

	out, mergeErr := m.runCmd(targetDir, "git", "merge", "--no-ff", sourceBranch)
	if mergeErr != nil {
		// Attempt to abort so the worktree is left in a clean state.
		_, _ = m.runCmd(targetDir, "git", "merge", "--abort")
		return &MergeConflictError{Output: out}
	}
	// Push the target branch so the orch branch on GitHub stays current.
	repoDir := filepath.Join(m.reposDir, repo)
	if out, pushErr := m.runCmd(repoDir, "git", "push", "-u", "origin", targetBranch); pushErr != nil {
		log.Printf("manager: git push origin %s after merge (non-fatal): %v\n%s", targetBranch, pushErr, out)
	}
	return nil
}

// CommitInfo describes a single commit returned by GetCommits.
type CommitInfo struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
}

// GetCommits returns the commits reachable from branch but not from the
// default branch (i.e. git log <default>..<branch>). It runs the command
// in the main repo directory. An empty slice (not an error) is returned
// when there are no such commits.
func (m *Manager) GetCommits(repo, branch string) ([]CommitInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	repoDir := filepath.Join(m.reposDir, repo)
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("repo %q not found", repo)
	}

	defaultBranch, err := getDefaultBranch(repoDir)
	if err != nil || defaultBranch == "" {
		defaultBranch = "main"
	}

	// Format: hash<TAB>subject, one per line.
	out, err := m.runCmd(repoDir, "git", "log",
		defaultBranch+".."+branch,
		"--pretty=format:%H\t%s",
	)
	if err != nil {
		// If the branch doesn't exist yet the command will fail — return empty.
		return []CommitInfo{}, nil
	}

	var commits []CommitInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		ci := CommitInfo{Hash: parts[0]}
		if len(parts) == 2 {
			ci.Subject = parts[1]
		}
		commits = append(commits, ci)
	}
	return commits, nil
}

// worktreeDirLocked resolves the worktree directory for repo/branch without
// acquiring the mutex (the caller must already hold it).
func (m *Manager) worktreeDirLocked(repo, branch string) (string, error) {
	repoDir := filepath.Join(m.reposDir, repo)
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		return "", fmt.Errorf("repo %q not found", repo)
	}

	defaultBranch, _ := getDefaultBranch(repoDir)
	if branch == defaultBranch {
		return repoDir, nil
	}

	wt := filepath.Join(m.reposDir, repo+"-"+branch)
	if _, err := os.Stat(wt); err == nil {
		return wt, nil
	}

	return "", fmt.Errorf("branch %q not found for repo %q", branch, repo)
}

// RemoveWorktree removes the worktree directory for the given branch.
func (m *Manager) RemoveWorktree(repo, branch string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	repoDir := filepath.Join(m.reposDir, repo)
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		return fmt.Errorf("repo %q not found", repo)
	}

	worktreeDir := filepath.Join(m.reposDir, repo+"-"+branch)

	// Ask git to deregister the worktree before removing the directory.
	if out, wtErr := m.runCmd(repoDir, "git", "worktree", "remove", "--force", worktreeDir); wtErr != nil {
		log.Printf("manager: git worktree remove %q (non-fatal): %v\n%s", worktreeDir, wtErr, out)
	}

	if err := os.RemoveAll(worktreeDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing worktree dir: %w", err)
	}
	return nil
}

// --- internal helpers -------------------------------------------------------

func getDefaultBranch(repoDir string) (string, error) {
	out, err := runCmdBasic(repoDir, "git", "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// listWorktrees returns BranchInfo for every worktree registered with the repo,
// including the main clone itself.
func listWorktrees(repoDir, repoName, defaultBranch string) ([]BranchInfo, error) {
	out, err := runCmdBasic(repoDir, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var branches []BranchInfo
	var curDir, curBranch string

	flush := func() {
		if curDir == "" {
			return
		}
		name := deriveBranchName(curDir, repoDir, repoName, defaultBranch, curBranch)
		branches = append(branches, BranchInfo{Name: name, Dir: curDir})
		curDir, curBranch = "", ""
	}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
			curDir = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			curBranch = strings.TrimPrefix(line, "branch ")
			curBranch = strings.TrimPrefix(curBranch, "refs/heads/")
		}
	}
	flush()

	return branches, nil
}

// deriveBranchName works out what branch name to assign to a worktree.
// For the main repo directory, it returns defaultBranch.
// For subdirectories named "<repo>-<branch>", it strips the prefix.
// Otherwise it falls back to gitBranch then the raw directory basename.
func deriveBranchName(worktreeDir, mainDir, repoName, defaultBranch, gitBranch string) string {
	if worktreeDir == mainDir {
		return defaultBranch
	}
	base := filepath.Base(worktreeDir)
	if prefix := repoName + "-"; strings.HasPrefix(base, prefix) {
		return strings.TrimPrefix(base, prefix)
	}
	if gitBranch != "" {
		return gitBranch
	}
	return base
}

func embedToken(repoURL, token string) string {
	for _, pfx := range []string{"https://", "http://"} {
		if strings.HasPrefix(repoURL, pfx) {
			return pfx + token + "@" + repoURL[len(pfx):]
		}
	}
	return repoURL
}

// runCmdBasic runs a git command without injecting auth credentials.
// Use this for read-only introspection commands (symbolic-ref, worktree list)
// that never require network access.
func runCmdBasic(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runCmd runs a git command as a method so it can inject authentication
// env vars from the manager's token when one is configured.
//
// When m.token is set, the following git config env vars are added:
//
//	GIT_CONFIG_COUNT=1
//	GIT_CONFIG_KEY_0=url.https://<token>@github.com/.insteadOf
//	GIT_CONFIG_VALUE_0=https://github.com/
//
// This rewrites every https://github.com/ URL to an authenticated one at the
// process level — no ~/.gitconfig mutations, no side effects between runs.
func (m *Manager) runCmd(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if m.token != "" {
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=url.https://"+m.token+"@github.com/.insteadOf",
			"GIT_CONFIG_VALUE_0=https://github.com/",
		)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}
