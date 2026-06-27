package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v62/github"
	"github.com/rs/zerolog/log"
	"plutoploy/plutoploy-gh-bot/config"
	ghclient "plutoploy/plutoploy-gh-bot/github"
	"plutoploy/plutoploy-gh-bot/store"
)

type Handler struct {
	cfg       *config.Config
	store     *store.InstallationStore
	clients   map[chan []byte]struct{}
	clientsMu sync.RWMutex
}

func NewHandler(cfg *config.Config, store *store.InstallationStore) *Handler {
	return &Handler{
		cfg:     cfg,
		store:   store,
		clients: make(map[chan []byte]struct{}),
	}
}

func (h *Handler) verifySignature(body []byte, signatureHeader string) bool {
	if h.cfg.WebhookSecret == "" {
		return true // No secret configured, skip verification
	}
	if signatureHeader == "" {
		return false
	}
	sig := strings.TrimPrefix(signatureHeader, "sha256=")
	mac := hmac.New(sha256.New, []byte(h.cfg.WebhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(sig), []byte(expected))
}

type WebhookEvent struct {
	Action      string
	Repo        string
	Owner       string
	RunID       int64
	RunName     string
	Status      string
	Conclusion  string
	Branch      string
	SHA         string
	CommitMsg   string
	Author      string
	Timestamp   time.Time
}

func (h *Handler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	// Verify webhook signature
	signature := r.Header.Get("X-Hub-Signature-256")
	if !h.verifySignature(body, signature) {
		log.Error().Msg("Invalid webhook signature")
		http.Error(w, "Invalid signature", http.StatusUnauthorized)
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	deliveryID := r.Header.Get("X-GitHub-Delivery")

	log.Info().
		Str("event", eventType).
		Str("delivery_id", deliveryID).
		Msg("Webhook received")

	payload, err := h.parsePayload(eventType, body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to parse payload")
		http.Error(w, "Failed to parse payload", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := h.processEvent(ctx, eventType, payload); err != nil {
		log.Error().Err(err).Str("event", eventType).Msg("Failed to process event")
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status": "ok"}`)
}

func (h *Handler) parsePayload(eventType string, body []byte) (interface{}, error) {
	switch eventType {
	case "workflow_run":
		var event github.WorkflowRunEvent
		if err := json.Unmarshal(body, &event); err != nil {
			return nil, err
		}
		return &event, nil

	case "push":
		var event github.PushEvent
		if err := json.Unmarshal(body, &event); err != nil {
			return nil, err
		}
		return &event, nil

	case "pull_request":
		var event github.PullRequestEvent
		if err := json.Unmarshal(body, &event); err != nil {
			return nil, err
		}
		return &event, nil

	case "installation":
		var event github.InstallationEvent
		if err := json.Unmarshal(body, &event); err != nil {
			return nil, err
		}
		return &event, nil

	default:
		return body, nil
	}
}

func (h *Handler) processEvent(ctx context.Context, eventType string, payload interface{}) error {
	switch p := payload.(type) {
	case *github.WorkflowRunEvent:
		return h.handleWorkflowRun(ctx, p)
	case *github.PushEvent:
		return h.handlePush(ctx, p)
	case *github.PullRequestEvent:
		return h.handlePullRequest(ctx, p)
	case *github.InstallationEvent:
		return h.handleInstallation(ctx, p)
	default:
		log.Info().Str("event", eventType).Msg("Unhandled event type")
		return nil
	}
}

func (h *Handler) handleWorkflowRun(ctx context.Context, event *github.WorkflowRunEvent) error {
	run := event.WorkflowRun
	owner := event.GetRepo().GetOwner().GetLogin()
	repo := event.GetRepo().GetName()

	we := WebhookEvent{
		Action:     event.GetAction(),
		Repo:       repo,
		Owner:      owner,
		RunID:      run.GetID(),
		RunName:    run.GetName(),
		Status:     run.GetStatus(),
		Conclusion: run.GetConclusion(),
		Branch:     run.GetHeadBranch(),
		SHA:        run.GetHeadSHA(),
		Timestamp:  time.Now(),
	}

	log.Info().
		Str("repo", fmt.Sprintf("%s/%s", owner, repo)).
		Int64("run_id", we.RunID).
		Str("status", we.Status).
		Str("conclusion", we.Conclusion).
		Msg("Workflow run event")

	if err := h.broadcastEvent(we); err != nil {
		return fmt.Errorf("failed to broadcast: %w", err)
	}

	return nil
}

func (h *Handler) handlePush(ctx context.Context, event *github.PushEvent) error {
	owner := event.GetRepo().GetOwner().GetLogin()
	repo := event.GetRepo().GetName()

	we := WebhookEvent{
		Action:    "push",
		Repo:      repo,
		Owner:     owner,
		Branch:    event.GetRef(),
		SHA:       event.GetAfter(),
		CommitMsg: event.GetHeadCommit().GetMessage(),
		Author:    event.GetSender().GetLogin(),
		Timestamp: time.Now(),
	}

	log.Info().
		Str("repo", fmt.Sprintf("%s/%s", owner, repo)).
		Str("branch", we.Branch).
		Msg("Push event")

	return h.broadcastEvent(we)
}

func (h *Handler) handlePullRequest(ctx context.Context, event *github.PullRequestEvent) error {
	owner := event.GetRepo().GetOwner().GetLogin()
	repo := event.GetRepo().GetName()

	we := WebhookEvent{
		Action:    event.GetAction(),
		Repo:      repo,
		Owner:     owner,
		Branch:    event.GetPullRequest().GetHead().GetRef(),
		SHA:       event.GetPullRequest().GetHead().GetSHA(),
		CommitMsg: event.GetPullRequest().GetTitle(),
		Author:    event.GetSender().GetLogin(),
		Timestamp: time.Now(),
	}

	log.Info().
		Str("repo", fmt.Sprintf("%s/%s", owner, repo)).
		Str("action", we.Action).
		Msg("Pull request event")

	return h.broadcastEvent(we)
}

func (h *Handler) handleInstallation(ctx context.Context, event *github.InstallationEvent) error {
	action := event.GetAction()
	installation := event.GetInstallation()
	installationID := installation.GetID()

	log.Info().
		Str("action", action).
		Int64("installation_id", installationID).
		Msg("Installation event")

	switch action {
	case "created", "reopened":
		inst := &store.Installation{
			ID:              installationID,
			AccountLogin:    installation.GetAccount().GetLogin(),
			AccountType:    installation.GetAccount().GetType(),
			RepositorySelection: installation.GetRepositorySelection(),
		}

		if err := h.store.Save(installationID, inst); err != nil {
			return fmt.Errorf("failed to save installation: %w", err)
		}

		log.Info().
			Int64("installation_id", installationID).
			Str("account", inst.AccountLogin).
			Msg("Installation saved")

	case "deleted":
		if err := h.store.Delete(installationID); err != nil {
			return fmt.Errorf("failed to delete installation: %w", err)
		}

		log.Info().
			Int64("installation_id", installationID).
			Msg("Installation removed")
	}

	return nil
}

func (h *Handler) broadcastEvent(event WebhookEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	log.Debug().
		Str("event_type", fmt.Sprintf("%T", event)).
		Bytes("data", data).
		Msg("Broadcasting event")

	h.clientsMu.RLock()
	defer h.clientsMu.RUnlock()

	for ch := range h.clients {
		select {
		case ch <- data:
		default:
			// Client too slow, drop event
		}
	}

	return nil
}

func (h *Handler) FetchAllRepos(w http.ResponseWriter, r *http.Request) {
	installationID := r.URL.Query().Get("installation_id")
	if installationID == "" {
		http.Error(w, "installation_id required", http.StatusBadRequest)
		return
	}

	var instID int64
	fmt.Sscanf(installationID, "%d", &instID)

	ghClient, err := ghclient.NewClient(h.cfg, instID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create client: %v", err), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repos, err := ghClient.GetAllRepos(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch repos: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(repos)
}

func (h *Handler) GetWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	installationID := r.URL.Query().Get("installation_id")
	owner := r.URL.Query().Get("owner")
	repo := r.URL.Query().Get("repo")

	if installationID == "" || owner == "" || repo == "" {
		http.Error(w, "installation_id, owner, repo required", http.StatusBadRequest)
		return
	}

	var instID int64
	fmt.Sscanf(installationID, "%d", &instID)

	ghClient, err := ghclient.NewClient(h.cfg, instID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create client: %v", err), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runs, err := ghClient.GetWorkflowRuns(ctx, owner, repo)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch runs: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(runs)
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

	var instID, runID int64
	fmt.Sscanf(installationID, "%d", &instID)
	fmt.Sscanf(runIDStr, "%d", &runID)

	ghClient, err := ghclient.NewClient(h.cfg, instID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create client: %v", err), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

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

	var instID, runID int64
	fmt.Sscanf(installationID, "%d", &instID)
	fmt.Sscanf(runIDStr, "%d", &runID)

	ghClient, err := ghclient.NewClient(h.cfg, instID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create client: %v", err), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	status, err := ghClient.GetWorkflowRunStatus(ctx, owner, repo, runID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch status: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
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

	ghClient, err := ghclient.NewClient(h.cfg, req.InstallationID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create client: %v", err), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := ghClient.InjectFile(ctx, req.Owner, req.Repo, req.Path, req.Content, req.Message, req.Branch); err != nil {
		http.Error(w, fmt.Sprintf("Failed to inject file: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) ListInstallations(w http.ResponseWriter, r *http.Request) {
	installations := h.store.List()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(installations)
}

// ServeEvents handles SSE connections for real-time event streaming.
func (h *Handler) ServeEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := make(chan []byte, 10)
	h.clientsMu.Lock()
	h.clients[ch] = struct{}{}
	h.clientsMu.Unlock()

	defer func() {
		h.clientsMu.Lock()
		delete(h.clients, ch)
		h.clientsMu.Unlock()
	}()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
