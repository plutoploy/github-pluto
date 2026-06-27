package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/go-github/v88/github"
	"github.com/palantir/go-githubapp/githubapp"
	"github.com/rs/zerolog/log"
	"plutoploy/plutoploy-gh-bot/sse"
	"plutoploy/plutoploy-gh-bot/store"
)

// EventHandlers returns the githubapp.EventHandler set backing the webhook
// dispatcher. They share the Handler's broker and store.
func (h *Handler) EventHandlers() []githubapp.EventHandler {
	return []githubapp.EventHandler{
		&workflowRunHandler{broker: h.broker},
		&pushHandler{broker: h.broker},
		&pullRequestHandler{broker: h.broker},
		&installationHandler{store: h.store},
	}
}

type workflowRunHandler struct{ broker *sse.Broker }

func (h *workflowRunHandler) Handles() []string { return []string{"workflow_run"} }

func (h *workflowRunHandler) Handle(ctx context.Context, eventType, deliveryID string, payload []byte) error {
	var event github.WorkflowRunEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("failed to parse workflow_run payload: %w", err)
	}

	run := event.GetWorkflowRun()
	owner := event.GetRepo().GetOwner().GetLogin()
	repo := event.GetRepo().GetName()

	e := Event{
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
		Int64("run_id", e.RunID).
		Str("status", e.Status).
		Str("conclusion", e.Conclusion).
		Msg("Workflow run event")

	publish(h.broker, e)
	return nil
}

type pushHandler struct{ broker *sse.Broker }

func (h *pushHandler) Handles() []string { return []string{"push"} }

func (h *pushHandler) Handle(ctx context.Context, eventType, deliveryID string, payload []byte) error {
	var event github.PushEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("failed to parse push payload: %w", err)
	}

	owner := event.GetRepo().GetOwner().GetLogin()
	repo := event.GetRepo().GetName()

	e := Event{
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
		Str("branch", e.Branch).
		Msg("Push event")

	publish(h.broker, e)
	return nil
}

type pullRequestHandler struct{ broker *sse.Broker }

func (h *pullRequestHandler) Handles() []string { return []string{"pull_request"} }

func (h *pullRequestHandler) Handle(ctx context.Context, eventType, deliveryID string, payload []byte) error {
	var event github.PullRequestEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("failed to parse pull_request payload: %w", err)
	}

	owner := event.GetRepo().GetOwner().GetLogin()
	repo := event.GetRepo().GetName()

	e := Event{
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
		Str("action", e.Action).
		Msg("Pull request event")

	publish(h.broker, e)
	return nil
}

type installationHandler struct{ store *store.InstallationStore }

func (h *installationHandler) Handles() []string { return []string{"installation"} }

func (h *installationHandler) Handle(ctx context.Context, eventType, deliveryID string, payload []byte) error {
	var event github.InstallationEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("failed to parse installation payload: %w", err)
	}

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
			ID:                  installationID,
			AccountLogin:        installation.GetAccount().GetLogin(),
			AccountType:         installation.GetAccount().GetType(),
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
