package webhook

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/palantir/go-githubapp/githubapp"
	"github.com/rs/zerolog/log"
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

// FetchAllRepos returns all repositories accessible to the given installation.
func (h *Handler) FetchAllRepos(c *gin.Context) {
	installationID := c.Query("installation_id")
	if installationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "installation_id required"})
		return
	}

	ghClient, err := h.newGitHubClient(parseInt64(installationID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create client: %v", err)})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	repos, err := ghClient.GetAllRepos(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to fetch repos: %v", err)})
		return
	}

	c.JSON(http.StatusOK, repos)
}

// GetWorkflowRuns returns the list of workflow runs for a repo.
func (h *Handler) GetWorkflowRuns(c *gin.Context) {
	installationID := c.Query("installation_id")
	owner := c.Query("owner")
	repo := c.Query("repo")

	if installationID == "" || owner == "" || repo == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "installation_id, owner, repo required"})
		return
	}

	ghClient, err := h.newGitHubClient(parseInt64(installationID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create client: %v", err)})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	runs, err := ghClient.GetWorkflowRuns(ctx, owner, repo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to fetch runs: %v", err)})
		return
	}

	c.JSON(http.StatusOK, runs)
}

// GetWorkflowLogs returns the logs zip for a specific workflow run.
func (h *Handler) GetWorkflowLogs(c *gin.Context) {
	installationID := c.Query("installation_id")
	owner := c.Query("owner")
	repo := c.Query("repo")
	runIDStr := c.Query("run_id")

	if installationID == "" || owner == "" || repo == "" || runIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "installation_id, owner, repo, run_id required"})
		return
	}

	ghClient, err := h.newGitHubClient(parseInt64(installationID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create client: %v", err)})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	runID := parseInt64(runIDStr)
	logs, err := ghClient.GetWorkflowRunLogs(ctx, owner, repo, runID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to fetch logs: %v", err)})
		return
	}

	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="logs-%d.zip"`, runID))
	if _, err := c.Writer.Write(logs); err != nil {
		log.Error().Err(err).Msg("Failed to write logs response")
	}
}

// GetWorkflowStatus returns the status of a specific workflow run.
func (h *Handler) GetWorkflowStatus(c *gin.Context) {
	installationID := c.Query("installation_id")
	owner := c.Query("owner")
	repo := c.Query("repo")
	runIDStr := c.Query("run_id")

	if installationID == "" || owner == "" || repo == "" || runIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "installation_id, owner, repo, run_id required"})
		return
	}

	ghClient, err := h.newGitHubClient(parseInt64(installationID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create client: %v", err)})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	status, err := ghClient.GetWorkflowRunStatus(ctx, owner, repo, parseInt64(runIDStr))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to fetch status: %v", err)})
		return
	}

	c.JSON(http.StatusOK, status)
}

// InjectFile creates or updates a file in a GitHub repository.
func (h *Handler) InjectFile(c *gin.Context) {
	var req struct {
		InstallationID int64  `json:"installation_id"`
		Owner          string `json:"owner"`
		Repo           string `json:"repo"`
		Path           string `json:"path"`
		Content        string `json:"content"`
		Message        string `json:"message"`
		Branch         string `json:"branch"`
	}

	// Limit body to 10 MiB to prevent memory exhaustion.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 10<<20)

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	if req.Owner == "" || req.Repo == "" || req.Path == "" || req.Content == "" || req.Message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "owner, repo, path, content, message required"})
		return
	}

	ghClient, err := h.newGitHubClient(req.InstallationID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create client: %v", err)})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	if err := ghClient.InjectFile(ctx, req.Owner, req.Repo, req.Path, req.Content, req.Message, req.Branch); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to inject file: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ListInstallations returns all registered GitHub App installations.
func (h *Handler) ListInstallations(c *gin.Context) {
	c.JSON(http.StatusOK, h.store.List())
}

// ServeEvents streams real-time events for a single user's room over SSE.
// Clients must pass ?owner=<account-login>; they only receive events whose
// owner matches, giving each user an isolated room.
func (h *Handler) ServeEvents(c *gin.Context) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Streaming not supported"})
		return
	}

	room := c.Query("owner")
	if room == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "owner required"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	sub := h.broker.Subscribe(room)
	defer h.broker.Unsubscribe(sub)

	// Tell the client the stream is open and prompt proxies to flush.
	if _, err := fmt.Fprintf(c.Writer, ": connected to room %s\n\n", room); err != nil {
		log.Warn().Err(err).Msg("SSE initial write failed")
		return
	}
	flusher.Flush()

	// Periodic comment frames keep the connection alive through proxies.
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			if _, err := fmt.Fprint(c.Writer, ": keepalive\n\n"); err != nil {
				log.Warn().Err(err).Msg("SSE keepalive write failed, closing stream")
				return
			}
			flusher.Flush()
		case data, ok := <-sub.Events():
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", data); err != nil {
				log.Warn().Err(err).Msg("SSE data write failed, closing stream")
				return
			}
			flusher.Flush()
		}
	}
}
