package database

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type GetWorkspaceFeatureByNameParams struct {
	WorkspaceID pgtype.UUID
	FeatureName string
}

const getWorkspaceFeatureByName = `
SELECT id, workspace_id, feature_id, feature_name, title, feature_status, current_stage, next_action,
       stages, source_path, source_hash, created_at, updated_at
FROM workspace_features
WHERE workspace_id = $1 AND feature_name = $2`

func (q *Queries) GetWorkspaceFeatureByName(ctx context.Context, arg GetWorkspaceFeatureByNameParams) (WorkspaceFeature, error) {
	row := q.db.QueryRow(ctx, getWorkspaceFeatureByName, arg.WorkspaceID, arg.FeatureName)
	var i WorkspaceFeature
	err := row.Scan(
		&i.ID,
		&i.WorkspaceID,
		&i.FeatureID,
		&i.FeatureName,
		&i.Title,
		&i.FeatureStatus,
		&i.CurrentStage,
		&i.NextAction,
		&i.Stages,
		&i.SourcePath,
		&i.SourceHash,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

type GetWorkspaceTaskByNameParams struct {
	WorkspaceID pgtype.UUID
	FeatureID   pgtype.UUID
	TaskName    string
}

const getWorkspaceTaskByName = `
SELECT id, workspace_id, feature_id, feature_name, task_id, task_name, title, repo, status, depends_on,
       blocked_reason, branch, execution, pr, workspace_pr, source_path, source_hash,
       created_at, updated_at
FROM workspace_tasks
WHERE workspace_id = $1 AND feature_id = $2 AND task_name = $3`

func (q *Queries) GetWorkspaceTaskByName(ctx context.Context, arg GetWorkspaceTaskByNameParams) (WorkspaceTask, error) {
	row := q.db.QueryRow(ctx, getWorkspaceTaskByName, arg.WorkspaceID, arg.FeatureID, arg.TaskName)
	var i WorkspaceTask
	err := row.Scan(
		&i.ID,
		&i.WorkspaceID,
		&i.FeatureID,
		&i.FeatureName,
		&i.TaskID,
		&i.TaskName,
		&i.Title,
		&i.Repo,
		&i.Status,
		&i.DependsOn,
		&i.BlockedReason,
		&i.Branch,
		&i.Execution,
		&i.Pr,
		&i.WorkspacePr,
		&i.SourcePath,
		&i.SourceHash,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}
