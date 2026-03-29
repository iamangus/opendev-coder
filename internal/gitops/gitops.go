package gitops

import "context"

type GitOps interface {
	Clone(ctx context.Context, url, dir string) error
	Fetch(ctx context.Context, dir string) error
	WorktreeAdd(ctx context.Context, repoDir, wtDir, branch string) error
	WorktreeRemove(ctx context.Context, repoDir, wtDir string) error
	Merge(ctx context.Context, dir, branch string) error
	Push(ctx context.Context, dir, branch string) error
	Diff(ctx context.Context, dir string) (string, error)
	CommitLog(ctx context.Context, dir string, args ...string) (string, error)
	DefaultBranch(ctx context.Context, dir string) (string, error)
	BranchExists(ctx context.Context, dir, branch string) (bool, error)
	RemoteBranchExists(ctx context.Context, dir, branch string) (bool, error)
	CreateBranch(ctx context.Context, dir, branch, startPoint string) error
	Status(ctx context.Context, dir string) (string, error)
}
