-- name: ListFeatureDocuments :many
SELECT id, workspace_id, feature_id, feature_name, document_type, source_path, url, created_at, updated_at
FROM workspace_feature_documents
WHERE workspace_id = $1 AND feature_id = $2
ORDER BY document_type;

-- name: ListWorkspaceFeatureDocuments :many
SELECT id, workspace_id, feature_id, feature_name, document_type, source_path, url, created_at, updated_at
FROM workspace_feature_documents
WHERE workspace_id = $1
ORDER BY feature_name, document_type;

-- name: UpsertFeatureDocument :one
INSERT INTO workspace_feature_documents (
    workspace_id, feature_id, feature_name, document_type, source_path, url, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, now(), now())
ON CONFLICT (workspace_id, feature_id, document_type) DO UPDATE SET
    feature_name  = EXCLUDED.feature_name,
    source_path   = EXCLUDED.source_path,
    url           = EXCLUDED.url,
    updated_at    = now()
RETURNING id, workspace_id, feature_id, feature_name, document_type, source_path, url, created_at, updated_at;

-- name: DeleteFeatureDocumentsNotIn :exec
DELETE FROM workspace_feature_documents
WHERE workspace_id = $1
  AND feature_id = $2
  AND document_type != ALL($3::text[]);

-- name: DeleteAllFeatureDocuments :exec
DELETE FROM workspace_feature_documents
WHERE workspace_id = $1 AND feature_id = $2;
