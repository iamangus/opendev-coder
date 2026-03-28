package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/iamangus/code-mcp/internal/config"
	githubpkg "github.com/iamangus/code-mcp/internal/github"
	"github.com/iamangus/code-mcp/internal/gitops"
	"github.com/iamangus/code-mcp/internal/locks"
	"github.com/iamangus/code-mcp/internal/manager"
	"github.com/iamangus/code-mcp/internal/tools"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	var (
		mode      = flag.String("mode", "stdio", "Transport mode: stdio or http (single-server mode only)")
		addr      = flag.String("addr", ":8080", "HTTP listen address")
		dir       = flag.String("dir", "", "Single-server mode: absolute path to the worktree root directory")
		reposDir  = flag.String("repos-dir", "/repos", "Multi-server mode: directory containing all repositories")
		logFormat = flag.String("log-format", "text", "log output format: text or json")
		logLevel  = flag.String("log-level", "info", "log level: debug, info, warn, error")
	)
	flag.Parse()

	var level slog.Level
	switch strings.ToLower(*logLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if *logFormat == "json" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)

	// ── Single-server (backward-compatible) mode ──────────────────────────
	if *dir != "" {
		runSingleServer(*mode, *addr, *dir, logger)
		return
	}

	// ── GitHub client (optional) ───────────────────────────────────────────
	// If GITHUB_TOKEN and GITHUB_OWNER are both set, a GitHub client is
	// constructed and passed to the multi-server so it can manage PRs.
	// The token is also passed to the manager so all git operations
	// (push/fetch) authenticate automatically.
	var ghClient githubpkg.Client
	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken != "" {
		if owner := os.Getenv("GITHUB_OWNER"); owner != "" {
			ghClient = githubpkg.NewHTTPClient(githubToken, owner, slog.Default())
			logger.Info("GitHub PR integration enabled", "owner", owner)
		} else {
			logger.Warn("GITHUB_TOKEN set but GITHUB_OWNER is missing, PR integration disabled")
		}
	}

	// ── Multi-server mode ──────────────────────────────────────────────────
	runMultiServer(*addr, *reposDir, githubToken, ghClient, logger)
}

// runSingleServer starts profile-aware MCP servers for a single worktree.
// Each profile is served at /{profile}/mcp on the same HTTP address.
// In stdio mode only the read profile is served (stdio is single-stream).
func runSingleServer(mode, addr, dir string, logger *slog.Logger) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "error: --dir %q does not exist or is not a directory\n", dir)
		os.Exit(1)
	}

	ts := tools.NewTestStore()

	switch mode {
	case "http":
		mux := http.NewServeMux()
		for _, p := range Profiles {
			h := newMCPHandler(p, dir, ts, logger)
			pattern := "/" + string(p) + "/mcp"
			mux.Handle(pattern, h)
			logger.Info("registered MCP handler", "profile", p, "dir", dir)
		}
		logger.Info("starting HTTP MCP server", "addr", addr, "dir", dir)
		if err := http.ListenAndServe(addr, mux); err != nil {
			fmt.Fprintf(os.Stderr, "HTTP server error: %v\n", err)
			os.Exit(1)
		}
	default:
		// stdio is inherently single-stream; serve the read profile.
		lm := locks.NewManager(slog.Default())
		s := server.NewMCPServer("code-mcp", "1.0.0", server.WithToolCapabilities(true))
		registerReadTools(s, lm, dir, logger)
		if err := server.ServeStdio(s); err != nil {
			fmt.Fprintf(os.Stderr, "stdio server error: %v\n", err)
			os.Exit(1)
		}
	}
}

// runMultiServer starts the multi-repo HTTP server.
//
// MCP endpoint layout:  http://host:port/{repo}/{branch}/{profile}/mcp
// Management API:       http://host:port/api/repos[/...]
func runMultiServer(addr, reposDir, githubToken string, ghClient githubpkg.Client, logger *slog.Logger) {
	gitOps := gitops.NewExec(slog.Default(), githubToken)
	mgr, err := manager.New(reposDir, gitOps, slog.Default())
	if err != nil {
		logger.Error("manager initialization failed", "error", err)
		os.Exit(1)
	}

	// Shared test store across all worktrees.
	ts := tools.NewTestStore()

	// handlers maps "repo/branch/profile" → http.Handler for that MCP server.
	var mu sync.RWMutex
	handlers := make(map[string]http.Handler)

	addHandlers := func(repo, branch, dir string) {
		mu.Lock()
		defer mu.Unlock()
		for _, p := range Profiles {
			key := repo + "/" + branch + "/" + string(p)
			handlers[key] = newMCPHandler(p, dir, ts, logger)
			logger.Info("registered MCP handler", "repo", repo, "branch", branch, "profile", p, "dir", dir)
		}
		// Auto-register the test command from .opendev/config.yaml if present.
		// Failures are logged but not fatal here — the branch creation REST
		// endpoint is responsible for failing loudly on missing config.
		cfg, err := config.Load(dir)
		if err != nil {
			logger.Warn("config: test command not registered", "repo", repo, "branch", branch, "error", err)
			return
		}
		if _, err := tools.RegisterTest(dir, cfg.TestCommand, "", ts); err != nil {
			logger.Warn("config: registering test command failed", "repo", repo, "branch", branch, "error", err)
			return
		}
		logger.Info("config: registered test command", "repo", repo, "branch", branch, "cmd", cfg.TestCommand)
	}

	removeHandlers := func(repo, branch string) {
		mu.Lock()
		defer mu.Unlock()
		for _, p := range Profiles {
			key := repo + "/" + branch + "/" + string(p)
			delete(handlers, key)
			logger.Info("unregistered MCP handler", "repo", repo, "branch", branch, "profile", p)
		}
	}

	// Discover existing repos on startup.
	repos, err := mgr.Scan()
	if err != nil {
		logger.Error("scanning repos failed", "error", err)
		os.Exit(1)
	}
	for _, repo := range repos {
		for _, b := range repo.Branches {
			addHandlers(repo.Name, b.Name, b.Dir)
		}
	}
	logger.Info("startup: repos discovered", "count", len(repos), "dir", reposDir)

	// Use two separate ServeMux instances to avoid Go 1.22+ pattern-conflict
	// panics between the catch-all MCP wildcard and the API routes.

	// MCP mux: /{repo}/{branch}/{profile}/mcp
	mcpMux := http.NewServeMux()
	mcpMux.HandleFunc("/{repo}/{branch}/{profile}/mcp", func(w http.ResponseWriter, r *http.Request) {
		repo := r.PathValue("repo")
		branch := r.PathValue("branch")
		profile := r.PathValue("profile")
		key := repo + "/" + branch + "/" + profile
		mu.RLock()
		h, ok := handlers[key]
		mu.RUnlock()
		if !ok {
			http.Error(w, fmt.Sprintf("no MCP server for %s/%s/%s", repo, branch, profile), http.StatusNotFound)
			return
		}
		h.ServeHTTP(w, r)
	})

	// API mux: /api/...
	apiMux := http.NewServeMux()
	registerAPIRoutes(apiMux, mgr, ts, ghClient, logger, addHandlers, removeHandlers)

	// Top-level handler: dispatch to API mux for /api/ paths, otherwise MCP mux.
	top := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			apiMux.ServeHTTP(w, r)
			return
		}
		mcpMux.ServeHTTP(w, r)
	})

	logger.Info("starting multi-server", "addr", addr, "repos_dir", reposDir)
	if err := http.ListenAndServe(addr, top); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}
