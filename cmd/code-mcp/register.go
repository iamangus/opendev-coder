package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"time"

	"github.com/iamangus/code-mcp/internal/locks"
	"github.com/iamangus/code-mcp/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Profile identifies a named set of MCP tools.
type Profile string

const (
	ProfileRead  Profile = "read"
	ProfileWrite Profile = "write"
)

// Profiles is the ordered list of all known profiles. A handler is created for
// each profile on every repo/branch.
var Profiles = []Profile{ProfileRead, ProfileWrite}

// registerReadTools registers the read-only tool set on s.
// Included: read_file, read_lines, list_directory, grep_search, get_git_diff.
func registerReadTools(s *server.MCPServer, lm *locks.Manager, worktreeRoot string) {
	// read_file
	s.AddTool(
		mcp.NewTool("read_file",
			mcp.WithDescription("Read the entire contents of a file within the worktree."),
			mcp.WithString("filepath", mcp.Required(), mcp.Description("Path to the file, relative to the worktree root.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			start := time.Now()
			fp, err := req.RequireString("filepath")
			if err != nil {
				log.Printf("tool=read_file error=%q elapsed=%dms", err, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(err.Error()), nil
			}
			content, toolErr := tools.ReadFile(ctx, worktreeRoot, fp, lm)
			if toolErr != nil {
				log.Printf("tool=read_file filepath=%q error=%q elapsed=%dms", fp, toolErr, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(toolErr.Error()), nil
			}
			log.Printf("tool=read_file filepath=%q ok elapsed=%dms", fp, time.Since(start).Milliseconds())
			return mcp.NewToolResultText(content), nil
		},
	)

	// read_lines
	s.AddTool(
		mcp.NewTool("read_lines",
			mcp.WithDescription("Read a range of lines from a file within the worktree (1-indexed, inclusive)."),
			mcp.WithString("filepath", mcp.Required(), mcp.Description("Path to the file, relative to the worktree root.")),
			mcp.WithNumber("start_line", mcp.Required(), mcp.Description("First line to read (1-indexed).")),
			mcp.WithNumber("end_line", mcp.Required(), mcp.Description("Last line to read (1-indexed, inclusive).")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			start := time.Now()
			fp, err := req.RequireString("filepath")
			if err != nil {
				log.Printf("tool=read_lines error=%q elapsed=%dms", err, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(err.Error()), nil
			}
			startLine := req.GetInt("start_line", 1)
			endLine := req.GetInt("end_line", 1)
			content, toolErr := tools.ReadLines(ctx, worktreeRoot, fp, startLine, endLine, lm)
			if toolErr != nil {
				log.Printf("tool=read_lines filepath=%q start=%d end=%d error=%q elapsed=%dms", fp, startLine, endLine, toolErr, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(toolErr.Error()), nil
			}
			log.Printf("tool=read_lines filepath=%q start=%d end=%d ok elapsed=%dms", fp, startLine, endLine, time.Since(start).Milliseconds())
			return mcp.NewToolResultText(content), nil
		},
	)

	// list_directory
	s.AddTool(
		mcp.NewTool("list_directory",
			mcp.WithDescription("List the contents of a directory within the worktree."),
			mcp.WithString("dirpath", mcp.Required(), mcp.Description("Path to the directory to list, relative to the worktree root.")),
			mcp.WithBoolean("recursive", mcp.Description("If true, list recursively. Default: false.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			start := time.Now()
			dirPath, err := req.RequireString("dirpath")
			if err != nil {
				log.Printf("tool=list_directory error=%q elapsed=%dms", err, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(err.Error()), nil
			}
			recursive := req.GetBool("recursive", false)
			listing, toolErr := tools.ListDirectory(ctx, worktreeRoot, dirPath, recursive, lm)
			if toolErr != nil {
				log.Printf("tool=list_directory dirpath=%q recursive=%t error=%q elapsed=%dms", dirPath, recursive, toolErr, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(toolErr.Error()), nil
			}
			log.Printf("tool=list_directory dirpath=%q recursive=%t ok elapsed=%dms", dirPath, recursive, time.Since(start).Milliseconds())
			return mcp.NewToolResultText(listing), nil
		},
	)

	// grep_search
	s.AddTool(
		mcp.NewTool("grep_search",
			mcp.WithDescription("Search for a pattern (regex or literal) within files in the worktree."),
			mcp.WithString("query", mcp.Required(), mcp.Description("Search pattern (regex or literal string).")),
			mcp.WithString("directory", mcp.Description("Optional subdirectory to search within, relative to the worktree root.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			start := time.Now()
			query, err := req.RequireString("query")
			if err != nil {
				log.Printf("tool=grep_search error=%q elapsed=%dms", err, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(err.Error()), nil
			}
			directory := req.GetString("directory", "")
			results, toolErr := tools.GrepSearch(ctx, worktreeRoot, query, directory, lm)
			if toolErr != nil {
				log.Printf("tool=grep_search query=%q directory=%q error=%q elapsed=%dms", query, directory, toolErr, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(toolErr.Error()), nil
			}
			log.Printf("tool=grep_search query=%q directory=%q ok elapsed=%dms", query, directory, time.Since(start).Milliseconds())
			return mcp.NewToolResultText(results), nil
		},
	)

	// get_git_diff
	s.AddTool(
		mcp.NewTool("get_git_diff",
			mcp.WithDescription("Get the git diff and status for the worktree."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			start := time.Now()
			diff, toolErr := tools.GetGitDiff(worktreeRoot)
			if toolErr != nil {
				log.Printf("tool=get_git_diff error=%q elapsed=%dms", toolErr, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(toolErr.Error()), nil
			}
			log.Printf("tool=get_git_diff ok elapsed=%dms", time.Since(start).Milliseconds())
			return mcp.NewToolResultText(diff), nil
		},
	)
}

// registerWriteTools registers the write/mutate tool set on s.
// Included: create_file, search_and_replace, execute_terminal_command, register_test.
func registerWriteTools(s *server.MCPServer, lm *locks.Manager, worktreeRoot string, ts *tools.TestStore) {
	// create_file
	s.AddTool(
		mcp.NewTool("create_file",
			mcp.WithDescription("Create a new file with specified content within the worktree. Fails if the file already exists."),
			mcp.WithString("filepath", mcp.Required(), mcp.Description("Path for the new file, relative to the worktree root.")),
			mcp.WithString("content", mcp.Required(), mcp.Description("Content to write to the new file.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			start := time.Now()
			fp, err := req.RequireString("filepath")
			if err != nil {
				log.Printf("tool=create_file error=%q elapsed=%dms", err, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(err.Error()), nil
			}
			content, err := req.RequireString("content")
			if err != nil {
				log.Printf("tool=create_file filepath=%q error=%q elapsed=%dms", fp, err, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(err.Error()), nil
			}
			msg, toolErr := tools.CreateFile(ctx, worktreeRoot, fp, content, lm)
			if toolErr != nil {
				log.Printf("tool=create_file filepath=%q error=%q elapsed=%dms", fp, toolErr, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(toolErr.Error()), nil
			}
			log.Printf("tool=create_file filepath=%q ok elapsed=%dms", fp, time.Since(start).Milliseconds())
			return mcp.NewToolResultText(msg), nil
		},
	)

	// search_and_replace
	s.AddTool(
		mcp.NewTool("search_and_replace",
			mcp.WithDescription("Find a block of text in a file and replace it. Uses exact match, then fuzzy match."),
			mcp.WithString("filepath", mcp.Required(), mcp.Description("Path to the file, relative to the worktree root.")),
			mcp.WithString("search_block", mcp.Required(), mcp.Description("The exact block of text to find.")),
			mcp.WithString("replace_block", mcp.Required(), mcp.Description("The text to replace the search_block with.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			start := time.Now()
			fp, err := req.RequireString("filepath")
			if err != nil {
				log.Printf("tool=search_and_replace error=%q elapsed=%dms", err, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(err.Error()), nil
			}
			searchBlock, err := req.RequireString("search_block")
			if err != nil {
				log.Printf("tool=search_and_replace filepath=%q error=%q elapsed=%dms", fp, err, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(err.Error()), nil
			}
			replaceBlock, err := req.RequireString("replace_block")
			if err != nil {
				log.Printf("tool=search_and_replace filepath=%q error=%q elapsed=%dms", fp, err, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(err.Error()), nil
			}
			result, toolErr := tools.SearchAndReplace(ctx, worktreeRoot, fp, searchBlock, replaceBlock, lm)
			if toolErr != nil {
				log.Printf("tool=search_and_replace filepath=%q error=%q elapsed=%dms", fp, toolErr, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(toolErr.Error()), nil
			}
			log.Printf("tool=search_and_replace filepath=%q ok elapsed=%dms", fp, time.Since(start).Milliseconds())
			return mcp.NewToolResultText(result), nil
		},
	)

	// execute_terminal_command
	s.AddTool(
		mcp.NewTool("execute_terminal_command",
			mcp.WithDescription("Execute a shell command in the worktree directory."),
			mcp.WithString("command", mcp.Required(), mcp.Description("Shell command to execute.")),
			mcp.WithNumber("timeout_seconds", mcp.Description("Maximum execution time in seconds. Default: 120.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			start := time.Now()
			command, err := req.RequireString("command")
			if err != nil {
				log.Printf("tool=execute_terminal_command error=%q elapsed=%dms", err, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(err.Error()), nil
			}
			timeoutSecs := req.GetInt("timeout_seconds", 120)
			timeout := time.Duration(timeoutSecs) * time.Second

			stdout, stderr, exitCode, timedOut, toolErr := tools.ExecuteTerminalCommand(worktreeRoot, command, timeout)
			if toolErr != nil {
				log.Printf("tool=execute_terminal_command command=%q error=%q elapsed=%dms", command, toolErr, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(toolErr.Error()), nil
			}

			var result string
			if timedOut {
				log.Printf("tool=execute_terminal_command command=%q timed_out=true elapsed=%dms", command, time.Since(start).Milliseconds())
				result = fmt.Sprintf("Command timed out after %d seconds.\nstdout: %s\nstderr: %s", timeoutSecs, stdout, stderr)
			} else {
				log.Printf("tool=execute_terminal_command command=%q exit_code=%d elapsed=%dms", command, exitCode, time.Since(start).Milliseconds())
				result = fmt.Sprintf("Exit code: %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
			}
			return mcp.NewToolResultText(result), nil
		},
	)

	// register_test
	s.AddTool(
		mcp.NewTool("register_test",
			mcp.WithDescription("Register a test command to be run against this worktree. The command will be executed from the worktree root directory. Only one test can be registered at a time; calling this again overwrites the previous registration."),
			mcp.WithString("command", mcp.Required(), mcp.Description("The shell command to run the tests (e.g. 'go test ./...', 'npm test', 'pytest').")),
			mcp.WithString("description", mcp.Description("Optional human-readable description of what the test verifies.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			start := time.Now()
			command, err := req.RequireString("command")
			if err != nil {
				log.Printf("tool=register_test error=%q elapsed=%dms", err, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(err.Error()), nil
			}
			description := req.GetString("description", "")
			msg, toolErr := tools.RegisterTest(worktreeRoot, command, description, ts)
			if toolErr != nil {
				log.Printf("tool=register_test error=%q elapsed=%dms", toolErr, time.Since(start).Milliseconds())
				return mcp.NewToolResultError(toolErr.Error()), nil
			}
			log.Printf("tool=register_test command=%q ok elapsed=%dms", command, time.Since(start).Milliseconds())
			return mcp.NewToolResultText(msg), nil
		},
	)
}

// newMCPHandler creates an http.Handler for the given profile, backed by a new
// MCP server instance constrained to worktreeRoot.
func newMCPHandler(profile Profile, worktreeRoot string, ts *tools.TestStore) *server.StreamableHTTPServer {
	lm := locks.NewManager(slog.Default())
	s := server.NewMCPServer("code-mcp", "1.0.0", server.WithToolCapabilities(true))
	switch profile {
	case ProfileRead:
		registerReadTools(s, lm, worktreeRoot)
	case ProfileWrite:
		registerWriteTools(s, lm, worktreeRoot, ts)
	}
	return server.NewStreamableHTTPServer(s)
}
