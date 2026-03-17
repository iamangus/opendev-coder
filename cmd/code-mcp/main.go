package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

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

	// ── Multi-server mode ──────────────────────────────────────────────────
	runMultiServer(*addr, *reposDir)
}

// runSingleServer is the original single-repo behaviour, kept for backward
// compatibility when --dir is provided.
func runSingleServer(mode, addr, dir string) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "error: --dir %q does not exist or is not a directory\n", dir)
		os.Exit(1)
	}

	lm := locks.NewManager()
	ts := tools.NewTestStore()
	s := server.NewMCPServer("code-mcp", "1.0.0", server.WithToolCapabilities(true))
	registerTools(s, lm, dir, ts)

	switch mode {
	case "http":
		httpSrv := server.NewStreamableHTTPServer(s)
		log.Printf("Starting HTTP MCP server on %s (dir=%s)", addr, dir)
		if err := httpSrv.Start(addr); err != nil {
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

// runMultiServer starts the multi-repo HTTP server.
//
// MCP endpoint layout:  http://host:port/{repo}/{branch}/mcp
// Management API:       http://host:port/api/repos[/...]
func runMultiServer(addr, reposDir string) {
	mgr, err := manager.New(reposDir)
	if err != nil {
		log.Fatalf("manager: %v", err)
	}

	// Shared test store across all worktrees.
	ts := tools.NewTestStore()

	// handlers maps "repo/branch" → http.Handler for that MCP server.
	var mu sync.RWMutex
	handlers := make(map[string]http.Handler)

	addHandler := func(repo, branch, dir string) {
		key := repo + "/" + branch
		h := newMCPHandler(dir, ts)
		mu.Lock()
		handlers[key] = h
		mu.Unlock()
		log.Printf("registered MCP handler for %s/%s -> %s", repo, branch, dir)
	}

	removeHandler := func(repo, branch string) {
		key := repo + "/" + branch
		mu.Lock()
		delete(handlers, key)
		mu.Unlock()
		log.Printf("unregistered MCP handler for %s/%s", repo, branch)
	}

	// Discover existing repos on startup.
	repos, err := mgr.Scan()
	if err != nil {
		log.Fatalf("scanning repos: %v", err)
	}
	for _, repo := range repos {
		for _, b := range repo.Branches {
			addHandler(repo.Name, b.Name, b.Dir)
		}
	}
	log.Printf("startup: found %d repo(s) in %s", len(repos), reposDir)

	// Use two separate ServeMux instances to avoid Go 1.22+ pattern-conflict
	// panics between the catch-all MCP wildcard and the API routes.

	// MCP mux: /{repo}/{branch}/mcp
	mcpMux := http.NewServeMux()
	mcpMux.HandleFunc("/{repo}/{branch}/mcp", func(w http.ResponseWriter, r *http.Request) {
		repo := r.PathValue("repo")
		branch := r.PathValue("branch")
		mu.RLock()
		h, ok := handlers[repo+"/"+branch]
		mu.RUnlock()
		if !ok {
			http.Error(w, fmt.Sprintf("no MCP server for %s/%s", repo, branch), http.StatusNotFound)
			return
		}
		h.ServeHTTP(w, r)
	})

	// API mux: /api/...
	apiMux := http.NewServeMux()
	registerAPIRoutes(apiMux, mgr, ts, addHandler, removeHandler)

	// Top-level handler: dispatch to API mux for /api/ paths, otherwise MCP mux.
	// We use a manual prefix check rather than a single ServeMux because Go
	// 1.22+ strict pattern-conflict detection panics when the wildcard MCP
	// route (/{repo}/{branch}/mcp) is registered alongside method-qualified API
	// routes (e.g. DELETE /api/repos/{repo}) in the same mux — both could match
	// a path like /api/repos/mcp.
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
