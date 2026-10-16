-- name: CreateAuditLog :one
INSERT INTO audit_logs (id, project_id, actor, action, target_type, target_id, detail)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: ListAuditLogs :many
SELECT * FROM audit_logs WHERE project_id = $1 ORDER BY created_at DESC LIMIT $2;

-- name: GetSpecTrace :many
SELECT t.id, t.title, t.status FROM tasks t WHERE t.spec_id = $1;
