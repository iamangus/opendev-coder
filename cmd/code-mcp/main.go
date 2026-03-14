package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/iamangus/code-mcp/internal/locks"
	"github.com/iamangus/code-mcp/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	var (
		mode = flag.String("mode", "stdio", "Transport mode: stdio or http")
		addr = flag.String("addr", ":8080", "HTTP listen address (used when mode=http)")
		dir  = flag.String("dir", "", "Absolute path to the worktree root directory (required)")
	)
	flag.Parse()

	if *dir == "" {
		fmt.Fprintln(os.Stderr, "error: --dir is required")
		flag.Usage()
		os.Exit(1)
	}
	info, err := os.Stat(*dir)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "error: --dir %q does not exist or is not a directory\n", *dir)
		os.Exit(1)
	}

	lm := locks.NewManager()

	s := server.NewMCPServer(
		"code-mcp",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	registerTools(s, lm, *dir)

	switch *mode {
	case "http":
		httpSrv := server.NewStreamableHTTPServer(s)
		log.Printf("Starting HTTP MCP server on %s", *addr)
		if err := httpSrv.Start(*addr); err != nil {
			fmt.Fprintf(os.Stderr, "HTTP server error: %v\n", err)
			os.Exit(1)
		}
	default:
		if err := server.ServeStdio(s); err != nil {
			fmt.Fprintf(os.Stderr, "stdio server error: %v\n", err)
			os.Exit(1)
		}
	}
}

func registerTools(s *server.MCPServer, lm *locks.Manager, worktreeRoot string) {
	// read_file
	s.AddTool(
		mcp.NewTool("read_file",
			mcp.WithDescription("Read the entire contents of a file within the worktree."),
			mcp.WithString("filepath", mcp.Required(), mcp.Description("Path to the file, relative to the worktree root.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			fp, err := req.RequireString("filepath")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			content, toolErr := tools.ReadFile(worktreeRoot, fp, lm)
			if toolErr != nil {
				return mcp.NewToolResultError(toolErr.Error()), nil
			}
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
			fp, err := req.RequireString("filepath")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			startLine := req.GetInt("start_line", 1)
			endLine := req.GetInt("end_line", 1)
			content, toolErr := tools.ReadLines(worktreeRoot, fp, startLine, endLine, lm)
			if toolErr != nil {
				return mcp.NewToolResultError(toolErr.Error()), nil
			}
			return mcp.NewToolResultText(content), nil
		},
	)

	// create_file
	s.AddTool(
		mcp.NewTool("create_file",
			mcp.WithDescription("Create a new file with specified content within the worktree. Fails if the file already exists."),
			mcp.WithString("filepath", mcp.Required(), mcp.Description("Path for the new file, relative to the worktree root.")),
			mcp.WithString("content", mcp.Required(), mcp.Description("Content to write to the new file.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			fp, err := req.RequireString("filepath")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			content, err := req.RequireString("content")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			msg, toolErr := tools.CreateFile(worktreeRoot, fp, content, lm)
			if toolErr != nil {
				return mcp.NewToolResultError(toolErr.Error()), nil
			}
			return mcp.NewToolResultText(msg), nil
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
			dirPath, err := req.RequireString("dirpath")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			recursive := req.GetBool("recursive", false)
			listing, toolErr := tools.ListDirectory(worktreeRoot, dirPath, recursive, lm)
			if toolErr != nil {
				return mcp.NewToolResultError(toolErr.Error()), nil
			}
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
			query, err := req.RequireString("query")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			directory := req.GetString("directory", "")
			results, toolErr := tools.GrepSearch(worktreeRoot, query, directory, lm)
			if toolErr != nil {
				return mcp.NewToolResultError(toolErr.Error()), nil
			}
			return mcp.NewToolResultText(results), nil
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
			fp, err := req.RequireString("filepath")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			searchBlock, err := req.RequireString("search_block")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			replaceBlock, err := req.RequireString("replace_block")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			result, toolErr := tools.SearchAndReplace(worktreeRoot, fp, searchBlock, replaceBlock, lm)
			if toolErr != nil {
				return mcp.NewToolResultError(toolErr.Error()), nil
			}
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
			command, err := req.RequireString("command")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			timeoutSecs := req.GetInt("timeout_seconds", 120)
			timeout := time.Duration(timeoutSecs) * time.Second

			stdout, stderr, exitCode, timedOut, toolErr := tools.ExecuteTerminalCommand(worktreeRoot, command, timeout)
			if toolErr != nil {
				return mcp.NewToolResultError(toolErr.Error()), nil
			}

			var result string
			if timedOut {
				result = fmt.Sprintf("Command timed out after %d seconds.\nstdout: %s\nstderr: %s", timeoutSecs, stdout, stderr)
			} else {
				result = fmt.Sprintf("Exit code: %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
			}
			return mcp.NewToolResultText(result), nil
		},
	)

	// get_git_diff
	s.AddTool(
		mcp.NewTool("get_git_diff",
			mcp.WithDescription("Get the git diff and status for the worktree."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			diff, toolErr := tools.GetGitDiff(worktreeRoot)
			if toolErr != nil {
				return mcp.NewToolResultError(toolErr.Error()), nil
			}
			return mcp.NewToolResultText(diff), nil
		},
	)
}
