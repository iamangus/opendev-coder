package worktree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ToolError is a user-facing error that should be returned as an MCP tool error.
type ToolError struct {
	Message string
}

func (e *ToolError) Error() string { return e.Message }

// Resolve resolves a relative path against worktreeRoot, ensuring it stays within the root.
func Resolve(worktreeRoot, relativePath string) (string, error) {
	if strings.ContainsRune(relativePath, 0) {
		return "", &ToolError{Message: "Tool Error: path contains null bytes"}
	}
	root, err := filepath.Abs(worktreeRoot)
	if err != nil {
		return "", fmt.Errorf("cannot resolve worktree root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", &ToolError{Message: fmt.Sprintf("Tool Error: worktree root %q does not exist or is not a directory", worktreeRoot)}
	}
	var abs string
	if filepath.IsAbs(relativePath) {
		abs = filepath.Clean(relativePath)
	} else {
		abs = filepath.Join(root, relativePath)
	}
	abs = filepath.Clean(abs)
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", &ToolError{Message: fmt.Sprintf("Tool Error: path %q escapes worktree root. All paths must be relative to the worktree root.", relativePath)}
	}
	return abs, nil
}
