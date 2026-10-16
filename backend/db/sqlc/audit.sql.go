package sqlc

import (
	"context"

	"github.com/google/uuid"
)

type CreateAuditLogParams struct {
	ID         uuid.UUID `json:"id"`
	ProjectID  uuid.UUID `json:"project_id"`
	Actor      string    `json:"actor"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type"`
	TargetID   uuid.UUID `json:"target_id"`
	Detail     []byte    `json:"detail"`
}

func (q *Queries) CreateAuditLog(ctx context.Context, arg CreateAuditLogParams) (*AuditLog, error) {
	row := q.pool.QueryRow(ctx, `INSERT INTO audit_logs (id, project_id, actor, action, target_type, target_id, detail) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, project_id, actor, action, target_type, target_id, detail, created_at`, arg.ID, arg.ProjectID, arg.Actor, arg.Action, arg.TargetType, arg.TargetID, arg.Detail)
	var a AuditLog
	err := row.Scan(&a.ID, &a.ProjectID, &a.Actor, &a.Action, &a.TargetType, &a.TargetID, &a.Detail, &a.CreatedAt)
	return &a, err
}

func (q *Queries) ListAuditLogs(ctx context.Context, projectID uuid.UUID, limit int32) ([]*AuditLog, error) {
	rows, err := q.pool.Query(ctx, `SELECT id, project_id, actor, action, target_type, target_id, detail, created_at FROM audit_logs WHERE project_id=$1 ORDER BY created_at DESC LIMIT $2`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*AuditLog
	for rows.Next() {
		var a AuditLog
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.Actor, &a.Action, &a.TargetType, &a.TargetID, &a.Detail, &a.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, &a)
	}
	return items, rows.Err()
}
