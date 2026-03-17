package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/iamangus/code-mcp/internal/manager"
	"github.com/iamangus/code-mcp/internal/tools"
)

// registerAPIRoutes registers the management REST API routes directly on mux.
//
// Routes:
//
//	GET    /api/repos                          – list all repos and their branches
//	POST   /api/repos                          – clone a new repo
//	DELETE /api/repos/{repo}                   – remove a repo and all its worktrees
//	GET    /api/repos/{repo}/branches          – list branches for a repo
//	POST   /api/repos/{repo}/branches          – create a worktree for a branch
//	DELETE /api/repos/{repo}/branches/{branch} – remove a worktree / notify of merge
func registerAPIRoutes(mux *http.ServeMux, mgr *manager.Manager, ts *tools.TestStore, onAdded func(repo, branch, dir string), onRemoved func(repo, branch string)) {
	// GET /api/repos
	mux.HandleFunc("GET /api/repos", func(w http.ResponseWriter, r *http.Request) {
		repos, err := mgr.Scan()
		if err != nil {
			apiError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"repos": repos})
	})

	// POST /api/repos  {"url":"...", "name":"...", "token":"..."}
	mux.HandleFunc("POST /api/repos", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			URL   string `json:"url"`
			Name  string `json:"name"`
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			apiError(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if body.URL == "" || body.Name == "" {
			apiError(w, "url and name are required", http.StatusBadRequest)
			return
		}
		if err := mgr.CloneRepo(body.URL, body.Name, body.Token); err != nil {
			apiError(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Scan to discover the default branch of the newly cloned repo.
		repos, _ := mgr.Scan()
		for _, repo := range repos {
			if repo.Name == body.Name {
				for _, b := range repo.Branches {
					onAdded(repo.Name, b.Name, b.Dir)
				}
				writeJSON(w, http.StatusCreated, map[string]any{"repo": repo})
				return
			}
		}
		writeJSON(w, http.StatusCreated, map[string]any{"repo": map[string]string{"name": body.Name}})
	})

	// DELETE /api/repos/{repo}
	mux.HandleFunc("DELETE /api/repos/{repo}", func(w http.ResponseWriter, r *http.Request) {
		repo := r.PathValue("repo")
		// Notify callers before removing so they can clean up handlers.
		repos, _ := mgr.Scan()
		for _, ri := range repos {
			if ri.Name == repo {
				for _, b := range ri.Branches {
					onRemoved(repo, b.Name)
				}
				break
			}
		}
		if err := mgr.RemoveRepo(repo); err != nil {
			apiError(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// GET /api/repos/{repo}/branches
	mux.HandleFunc("GET /api/repos/{repo}/branches", func(w http.ResponseWriter, r *http.Request) {
		repo := r.PathValue("repo")
		repos, err := mgr.Scan()
		if err != nil {
			apiError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, ri := range repos {
			if ri.Name == repo {
				writeJSON(w, http.StatusOK, map[string]any{"branches": ri.Branches})
				return
			}
		}
		apiError(w, "repo not found", http.StatusNotFound)
	})

	// POST /api/repos/{repo}/branches  {"branch":"..."}
	mux.HandleFunc("POST /api/repos/{repo}/branches", func(w http.ResponseWriter, r *http.Request) {
		repo := r.PathValue("repo")
		var body struct {
			Branch string `json:"branch"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			apiError(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if body.Branch == "" {
			apiError(w, "branch is required", http.StatusBadRequest)
			return
		}
		if err := mgr.CreateWorktree(repo, body.Branch); err != nil {
			apiError(w, err.Error(), http.StatusBadRequest)
			return
		}
		wtDir := mgr.BranchWorktreeDir(repo, body.Branch)
		onAdded(repo, body.Branch, wtDir)
		writeJSON(w, http.StatusCreated, map[string]any{
			"branch": manager.BranchInfo{Name: body.Branch, Dir: wtDir},
		})
	})

	// DELETE /api/repos/{repo}/branches/{branch}
	mux.HandleFunc("DELETE /api/repos/{repo}/branches/{branch}", func(w http.ResponseWriter, r *http.Request) {
		repo := r.PathValue("repo")
		branch := r.PathValue("branch")
		onRemoved(repo, branch)
		if err := mgr.RemoveWorktree(repo, branch); err != nil {
			apiError(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// POST /api/repos/{repo}/branches/{branch}/test/run
	mux.HandleFunc("POST /api/repos/{repo}/branches/{branch}/test/run", func(w http.ResponseWriter, r *http.Request) {
		repo := r.PathValue("repo")
		branch := r.PathValue("branch")

		wtDir, err := mgr.WorktreeDir(repo, branch)
		if err != nil {
			apiError(w, err.Error(), http.StatusNotFound)
			return
		}

		// Optional timeout from request body.
		var body struct {
			TimeoutSeconds int `json:"timeout_seconds"`
		}
		// Body is optional; ignore decode errors.
		_ = json.NewDecoder(r.Body).Decode(&body)

		timeout := tools.DefaultTimeout
		if body.TimeoutSeconds > 0 {
			timeout = time.Duration(body.TimeoutSeconds) * time.Second
		}

		result, err := tools.RunRegisteredTest(wtDir, ts, timeout)
		if err != nil {
			apiError(w, err.Error(), http.StatusNotFound)
			return
		}

		writeJSON(w, http.StatusOK, result)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("api: writeJSON encode error: %v", err)
	}
}

func apiError(w http.ResponseWriter, msg string, status int) {
	writeJSON(w, status, map[string]any{"error": msg})
}
