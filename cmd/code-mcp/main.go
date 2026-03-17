package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/iamangus/code-mcp/internal/config"
	githubpkg "github.com/iamangus/code-mcp/internal/github"
	"github.com/iamangus/code-mcp/internal/locks"
	"github.com/iamangus/code-mcp/internal/manager"
	"github.com/iamangus/code-mcp/internal/tools"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	var (
		mode     = flag.String("mode", "stdio", "Transport mode: stdio or http (single-server mode only)")
		addr     = flag.String("addr", ":8080", "HTTP listen address")
		dir      = flag.String("dir", "", "Single-server mode: absolute path to the worktree root directory")
		reposDir = flag.String("repos-dir", "/repos", "Multi-server mode: directory containing all repositories")
	)
	flag.Parse()

	// ── Single-server (backward-compatible) mode ──────────────────────────
	if *dir != "" {
		runSingleServer(*mode, *addr, *dir)
		return
	}

	// ── GitHub client (optional) ───────────────────────────────────────────
	// If GITHUB_TOKEN and GITHUB_OWNER are both set, a GitHub client is
	// constructed and passed to the multi-server so it can manage PRs.
	// The token is also passed to the manager so all git operations
	// (push/fetch) authenticate automatically.
	var ghClient *githubpkg.Client
	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken != "" {
		if owner := os.Getenv("GITHUB_OWNER"); owner != "" {
			ghClient = githubpkg.NewClient(githubToken, owner)
			log.Printf("GitHub PR integration enabled (owner: %s)", owner)
		} else {
			log.Printf("warning: GITHUB_TOKEN set but GITHUB_OWNER is missing — PR integration disabled")
		}
	}

	// ── Multi-server mode ──────────────────────────────────────────────────
	runMultiServer(*addr, *reposDir, githubToken, ghClient)
}

// runSingleServer starts profile-aware MCP servers for a single worktree.
// Each profile is served at /{profile}/mcp on the same HTTP address.
// In stdio mode only the read profile is served (stdio is single-stream).
func runSingleServer(mode, addr, dir string) {
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
			h := newMCPHandler(p, dir, ts)
			pattern := "/" + string(p) + "/mcp"
			mux.Handle(pattern, h)
			log.Printf("registered /%s/mcp (dir=%s)", p, dir)
		}
		log.Printf("starting HTTP MCP server on %s (dir=%s)", addr, dir)
		if err := http.ListenAndServe(addr, mux); err != nil {
			fmt.Fprintf(os.Stderr, "HTTP server error: %v\n", err)
			os.Exit(1)
		}
	default:
		// stdio is inherently single-stream; serve the read profile.
		lm := locks.NewManager()
		s := server.NewMCPServer("code-mcp", "1.0.0", server.WithToolCapabilities(true))
		registerReadTools(s, lm, dir)
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
func runMultiServer(addr, reposDir, githubToken string, ghClient *githubpkg.Client) {
	mgr, err := manager.New(reposDir, githubToken)
	if err != nil {
		log.Fatalf("manager: %v", err)
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
			handlers[key] = newMCPHandler(p, dir, ts)
			log.Printf("registered MCP handler for %s/%s/%s -> %s", repo, branch, p, dir)
		}
		// Auto-register the test command from .opendev/config.yaml if present.
		// Failures are logged but not fatal here — the branch creation REST
		// endpoint is responsible for failing loudly on missing config.
		cfg, err := config.Load(dir)
		if err != nil {
			log.Printf("config: %s/%s: %v (test command not registered)", repo, branch, err)
			return
		}
		if _, err := tools.RegisterTest(dir, cfg.TestCommand, "", ts); err != nil {
			log.Printf("config: %s/%s: registering test command: %v", repo, branch, err)
			return
		}
		log.Printf("config: %s/%s: registered test command: %s", repo, branch, cfg.TestCommand)
	}

	removeHandlers := func(repo, branch string) {
		mu.Lock()
		defer mu.Unlock()
		for _, p := range Profiles {
			key := repo + "/" + branch + "/" + string(p)
			delete(handlers, key)
			log.Printf("unregistered MCP handler for %s/%s/%s", repo, branch, p)
		}
	}

	// Discover existing repos on startup.
	repos, err := mgr.Scan()
	if err != nil {
		log.Fatalf("scanning repos: %v", err)
	}
	for _, repo := range repos {
		for _, b := range repo.Branches {
			addHandlers(repo.Name, b.Name, b.Dir)
		}
	}
	log.Printf("startup: found %d repo(s) in %s", len(repos), reposDir)

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
	registerAPIRoutes(apiMux, mgr, ts, ghClient, addHandlers, removeHandlers)

	// Top-level handler: dispatch to API mux for /api/ paths, otherwise MCP mux.
	top := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			apiMux.ServeHTTP(w, r)
			return
		}
		mcpMux.ServeHTTP(w, r)
	})

	log.Printf("starting multi-server on %s  (repos-dir=%s)", addr, reposDir)
	if err := http.ListenAndServe(addr, top); err != nil {
		log.Fatalf("server: %v", err)
	}
}
