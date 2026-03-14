# code-mcp

A Go MCP server that exposes coding tools scoped to a single Git worktree (branch).

## Usage

The server must be started with `--dir` pointing at the worktree root. All tool calls operate within that directory — callers do not pass a path to the worktree.

```
code-mcp --dir /path/to/worktree [--mode stdio|http] [--addr :8080]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dir` | *(required)* | Absolute path to the worktree root directory |
| `--mode` | `stdio` | Transport mode: `stdio` or `http` |
| `--addr` | `:8080` | HTTP listen address (only used when `--mode=http`) |

## Tools

| Tool | Parameters | Description |
|------|------------|-------------|
| `read_file` | `filepath` | Read the entire contents of a file |
| `read_lines` | `filepath`, `start_line`, `end_line` | Read a line range (1-indexed, inclusive) |
| `create_file` | `filepath`, `content` | Create a new file (fails if it already exists) |
| `list_directory` | `dirpath`, `recursive` | List directory contents |
| `grep_search` | `query`, `directory?` | Search for a regex/literal pattern across files |
| `search_and_replace` | `filepath`, `search_block`, `replace_block` | Replace a block of text (exact then fuzzy) |
| `execute_terminal_command` | `command`, `timeout_seconds?` | Run a shell command in the worktree |
| `get_git_diff` | *(none)* | Show `git diff HEAD` and `git status --short` |

All `filepath` and `dirpath` values are relative to the `--dir` worktree root. Path traversal outside the root is rejected.
