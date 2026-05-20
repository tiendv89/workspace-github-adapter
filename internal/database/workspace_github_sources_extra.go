package database

import (
	"context"
)

// GetGitHubSourceByRepoParams holds the parameters for GetGitHubSourceByRepo.
type GetGitHubSourceByRepoParams struct {
	RepoOwner string
	RepoName  string
}

const getGitHubSourceByRepo = `
SELECT id, workspace_id, repo_url, repo_owner, repo_name, default_branch, created_at, updated_at
FROM workspace_github_sources
WHERE repo_owner = $1 AND repo_name = $2
LIMIT 1`

// GetGitHubSourceByRepo looks up a GitHub source record by owner/repo name.
// Used by the webhook handler to identify which workspace a push event belongs to.
func (q *Queries) GetGitHubSourceByRepo(ctx context.Context, arg GetGitHubSourceByRepoParams) (WorkspaceGitHubSource, error) {
	row := q.db.QueryRow(ctx, getGitHubSourceByRepo, arg.RepoOwner, arg.RepoName)
	var i WorkspaceGitHubSource
	err := row.Scan(
		&i.ID,
		&i.WorkspaceID,
		&i.RepoURL,
		&i.RepoOwner,
		&i.RepoName,
		&i.DefaultBranch,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const listAllGitHubSources = `
SELECT id, workspace_id, repo_url, repo_owner, repo_name, default_branch, created_at, updated_at
FROM workspace_github_sources`

// ListAllGitHubSources returns all rows from workspace_github_sources in a single query.
// Used by ListWorkspaces to avoid N+1 lookups.
func (q *Queries) ListAllGitHubSources(ctx context.Context) ([]WorkspaceGitHubSource, error) {
	rows, err := q.db.Query(ctx, listAllGitHubSources)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []WorkspaceGitHubSource
	for rows.Next() {
		var i WorkspaceGitHubSource
		if err := rows.Scan(
			&i.ID,
			&i.WorkspaceID,
			&i.RepoURL,
			&i.RepoOwner,
			&i.RepoName,
			&i.DefaultBranch,
			&i.CreatedAt,
			&i.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}
