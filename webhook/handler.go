package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/palantir/go-githubapp/githubapp"
	ghclient "plutoploy/plutoploy-gh-bot/github"
	"plutoploy/plutoploy-gh-bot/sse"
	"plutoploy/plutoploy-gh-bot/store"
)

// Handler exposes the REST and SSE endpoints. Webhook delivery is handled
// separately by the githubapp EventDispatcher driving the EventHandlers in
// events.go; both share the same store and SSE broker.
type Handler struct {
	clientCreator githubapp.ClientCreator
	store         *store.InstallationStore
	broker        *sse.Broker
}

func NewHandler(cc githubapp.ClientCreator, store *store.InstallationStore) *Handler {
	return &Handler{
		clientCreator: cc,
		store:         store,
		broker:        sse.NewBroker(),
	}
}

// Broker returns the shared SSE broker so webhook event handlers can publish
// to the same rooms that SSE clients subscribe to.
func (h *Handler) Broker() *sse.Broker { return h.broker }

// newGitHubClient builds an installation-authenticated client.
func (h *Handler) newGitHubClient(installationID int64) (*ghclient.Client, error) {
	return ghclient.NewClient(h.clientCreator, installationID)
}

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (h *Handler) FetchAllRepos(w http.ResponseWriter, r *http.Request) {
	installationID := r.URL.Query().Get("installation_id")
	if installationID == "" {
		http.Error(w, "installation_id required", http.StatusBadRequest)
		return
	}

	ghClient, err := h.newGitHubClient(parseInt64(installationID))
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create client: %v", err), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	repos, err := ghClient.GetAllRepos(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch repos: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, repos)
}

func (h *Handler) GetWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	installationID := r.URL.Query().Get("installation_id")
	owner := r.URL.Query().Get("owner")
	repo := r.URL.Query().Get("repo")

	if installationID == "" || owner == "" || repo == "" {
		http.Error(w, "installation_id, owner, repo required", http.StatusBadRequest)
		return
	}

	ghClient, err := h.newGitHubClient(parseInt64(installationID))
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create client: %v", err), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	runs, err := ghClient.GetWorkflowRuns(ctx, owner, repo)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch runs: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, runs)
}

func (h *Handler) GetWorkflowLogs(w http.ResponseWriter, r *http.Request) {
	installationID := r.URL.Query().Get("installation_id")
	owner := r.URL.Query().Get("owner")
	repo := r.URL.Query().Get("repo")
	runIDStr := r.URL.Query().Get("run_id")

	if installationID == "" || owner == "" || repo == "" || runIDStr == "" {
		http.Error(w, "installation_id, owner, repo, run_id required", http.StatusBadRequest)
		return
	}

	ghClient, err := h.newGitHubClient(parseInt64(installationID))
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create client: %v", err), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	runID := parseInt64(runIDStr)
	logs, err := ghClient.GetWorkflowRunLogs(ctx, owner, repo, runID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch logs: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="logs-%d.zip"`, runID))
	w.Write(logs)
}

func (h *Handler) GetWorkflowStatus(w http.ResponseWriter, r *http.Request) {
	installationID := r.URL.Query().Get("installation_id")
	owner := r.URL.Query().Get("owner")
	repo := r.URL.Query().Get("repo")
	runIDStr := r.URL.Query().Get("run_id")

	if installationID == "" || owner == "" || repo == "" || runIDStr == "" {
		http.Error(w, "installation_id, owner, repo, run_id required", http.StatusBadRequest)
		return
	}

	ghClient, err := h.newGitHubClient(parseInt64(installationID))
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create client: %v", err), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	status, err := ghClient.GetWorkflowRunStatus(ctx, owner, repo, parseInt64(runIDStr))
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch status: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, status)
}

func (h *Handler) InjectFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		InstallationID int64  `json:"installation_id"`
		Owner          string `json:"owner"`
		Repo           string `json:"repo"`
		Path           string `json:"path"`
		Content        string `json:"content"`
		Message        string `json:"message"`
		Branch         string `json:"branch"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Owner == "" || req.Repo == "" || req.Path == "" || req.Content == "" || req.Message == "" {
		http.Error(w, "owner, repo, path, content, message required", http.StatusBadRequest)
		return
	}

	ghClient, err := h.newGitHubClient(req.InstallationID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create client: %v", err), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if err := ghClient.InjectFile(ctx, req.Owner, req.Repo, req.Path, req.Content, req.Message, req.Branch); err != nil {
		http.Error(w, fmt.Sprintf("Failed to inject file: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"status": "ok"})
}

func (h *Handler) ListInstallations(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.store.List())
}

// ServeEvents streams real-time events for a single user's room over SSE.
// Clients must pass ?owner=<account-login>; they only receive events whose
// owner matches, giving each user an isolated room.
func (h *Handler) ServeEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	room := r.URL.Query().Get("owner")
	if room == "" {
		http.Error(w, "owner required", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	sub := h.broker.Subscribe(room)
	defer h.broker.Unsubscribe(sub)

	// Tell the client the stream is open and prompt proxies to flush.
	fmt.Fprintf(w, ": connected to room %s\n\n", room)
	flusher.Flush()

	// Periodic comment frames keep the connection alive through proxies.
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case data, ok := <-sub.Events():
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
