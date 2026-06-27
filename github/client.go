package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v62/github"
	"github.com/rs/zerolog/log"
	"plutoploy/plutoploy-gh-bot/config"
)

type Client struct {
	*github.Client
}

func NewClient(cfg *config.Config, installationID int64) (*Client, error) {
	privateKey, err := os.ReadFile(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key: %w", err)
	}

	itr, err := ghinstallation.New(
		http.DefaultTransport,
		cfg.AppID,
		installationID,
		privateKey,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create installation transport: %w", err)
	}

	client := github.NewClient(&http.Client{Transport: itr})
	return &Client{Client: client}, nil
}

type Repository struct {
	Name     string
	FullName string
	Owner    string
	Private  bool
	CloneURL string
	HTMLURL  string
}

func (c *Client) GetAllRepos(ctx context.Context) ([]Repository, error) {
	var repos []Repository
	opts := &github.RepositoryListOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}

	for {
		repoList, resp, err := c.Repositories.List(ctx, "", opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list repos: %w", err)
		}

		for _, r := range repoList {
			repos = append(repos, Repository{
				Name:     r.GetName(),
				FullName: r.GetFullName(),
				Owner:    r.GetOwner().GetLogin(),
				Private:  r.GetPrivate(),
				CloneURL: r.GetCloneURL(),
				HTMLURL:  r.GetHTMLURL(),
			})
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return repos, nil
}

type WorkflowRun struct {
	ID         int64
	Name       string
	Status     string
	Conclusion string
	HeadBranch string
	HeadSHA    string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	RunNumber  int
	HTMLURL    string
}

func (c *Client) GetWorkflowRuns(ctx context.Context, owner, repo string) ([]WorkflowRun, error) {
	var runs []WorkflowRun
	opts := &github.ListWorkflowRunsOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}

	for {
		result, resp, err := c.Actions.ListRepositoryWorkflowRuns(ctx, owner, repo, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list workflow runs: %w", err)
		}

		for _, r := range result.WorkflowRuns {
			runs = append(runs, WorkflowRun{
				ID:         r.GetID(),
				Name:       r.GetName(),
				Status:     r.GetStatus(),
				Conclusion: r.GetConclusion(),
				HeadBranch: r.GetHeadBranch(),
				HeadSHA:    r.GetHeadSHA(),
				CreatedAt:  r.GetCreatedAt().Time,
				UpdatedAt:  r.GetUpdatedAt().Time,
				RunNumber:  r.GetRunNumber(),
				HTMLURL:    r.GetHTMLURL(),
			})
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return runs, nil
}

func (c *Client) GetWorkflowRunLogs(ctx context.Context, owner, repo string, runID int64) ([]byte, error) {
	url := fmt.Sprintf("/repos/%s/%s/actions/runs/%d/logs", owner, repo, runID)
	req, err := c.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.Do(ctx, req, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get logs: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read logs: %w", err)
	}

	return body, nil
}

func (c *Client) GetWorkflowRunStatus(ctx context.Context, owner, repo string, runID int64) (*WorkflowRun, error) {
	run, _, err := c.Actions.GetWorkflowRunByID(ctx, owner, repo, runID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workflow run: %w", err)
	}

	return &WorkflowRun{
		ID:         run.GetID(),
		Name:       run.GetName(),
		Status:     run.GetStatus(),
		Conclusion: run.GetConclusion(),
		HeadBranch: run.GetHeadBranch(),
		HeadSHA:    run.GetHeadSHA(),
		CreatedAt:  run.GetCreatedAt().Time,
		UpdatedAt:  run.GetUpdatedAt().Time,
		RunNumber:  run.GetRunNumber(),
		HTMLURL:    run.GetHTMLURL(),
	}, nil
}

func (c *Client) InjectFile(ctx context.Context, owner, repo, path, content, message, branch string) error {
	// Check if file already exists
	getOpts := &github.RepositoryContentGetOptions{}
	if branch != "" {
		getOpts.Ref = branch
	}

	existingFile, _, _, err := c.Repositories.GetContents(ctx, owner, repo, path, getOpts)

	if err == nil && existingFile != nil {
		// File exists — update it
		updateOpts := &github.RepositoryContentFileOptions{
			Message: github.String(message),
			Content: []byte(content),
			SHA:     existingFile.SHA,
		}
		if branch != "" {
			updateOpts.Branch = github.String(branch)
		}

		_, _, err = c.Repositories.UpdateFile(ctx, owner, repo, path, updateOpts)
		if err != nil {
			return fmt.Errorf("failed to update file: %w", err)
		}

		log.Info().
			Str("repo", fmt.Sprintf("%s/%s", owner, repo)).
			Str("path", path).
			Str("branch", branch).
			Msg("File updated successfully")
	} else {
		// File doesn't exist — create it
		createOpts := &github.RepositoryContentFileOptions{
			Message: github.String(message),
			Content: []byte(content),
		}
		if branch != "" {
			createOpts.Branch = github.String(branch)
		}

		_, _, err = c.Repositories.CreateFile(ctx, owner, repo, path, createOpts)
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}

		log.Info().
			Str("repo", fmt.Sprintf("%s/%s", owner, repo)).
			Str("path", path).
			Str("branch", branch).
			Msg("File created successfully")
	}

	return nil
}
