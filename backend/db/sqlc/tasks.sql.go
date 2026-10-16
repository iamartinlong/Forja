package sqlc

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type CreateTaskParams struct {
	ID             uuid.UUID   `json:"id"`
	ProjectID      uuid.UUID   `json:"project_id"`
	SpecID         pgtype.UUID `json:"spec_id"`
	Title          string      `json:"title"`
	Description    pgtype.Text `json:"description"`
	Priority       int32       `json:"priority"`
	EstimatedHours pgtype.Numeric `json:"estimated_hours"`
	AssignedTo     pgtype.UUID `json:"assigned_to"`
}

func (q *Queries) CreateTask(ctx context.Context, arg CreateTaskParams) (*Task, error) {
	row := q.pool.QueryRow(ctx, `INSERT INTO tasks (id, project_id, spec_id, title, description, status, priority, estimated_hours, assigned_to) VALUES ($1,$2,$3,$4,$5,'PENDING',$6,$7,$8) RETURNING id, project_id, spec_id, spec_version, title, description, status, priority, estimated_hours, assigned_to, confidence, action_required, created_at, updated_at`, arg.ID, arg.ProjectID, arg.SpecID, arg.Title, arg.Description, arg.Priority, arg.EstimatedHours, arg.AssignedTo)
	var t Task
	err := row.Scan(&t.ID, &t.ProjectID, &t.SpecID, &t.SpecVersion, &t.Title, &t.Description, &t.Status, &t.Priority, &t.EstimatedHours, &t.AssignedTo, &t.Confidence, &t.ActionRequired, &t.CreatedAt, &t.UpdatedAt)
	return &t, err
}

func (q *Queries) GetTask(ctx context.Context, id uuid.UUID) (*Task, error) {
	row := q.pool.QueryRow(ctx, `SELECT id, project_id, spec_id, spec_version, title, description, status, priority, estimated_hours, assigned_to, confidence, action_required, created_at, updated_at FROM tasks WHERE id=$1`, id)
	var t Task
	err := row.Scan(&t.ID, &t.ProjectID, &t.SpecID, &t.SpecVersion, &t.Title, &t.Description, &t.Status, &t.Priority, &t.EstimatedHours, &t.AssignedTo, &t.Confidence, &t.ActionRequired, &t.CreatedAt, &t.UpdatedAt)
	return &t, err
}

type ListTasksParams struct {
	ProjectID uuid.UUID   `json:"project_id"`
	Status    pgtype.Text `json:"status"`
	Limit     int32       `json:"limit"`
}

func (q *Queries) ListTasks(ctx context.Context, arg ListTasksParams) ([]*Task, error) {
	rows, err := q.pool.Query(ctx, `SELECT id, project_id, spec_id, spec_version, title, description, status, priority, estimated_hours, assigned_to, confidence, action_required, created_at, updated_at FROM tasks WHERE project_id=$1 AND ($2::varchar IS NULL OR status=$2) ORDER BY priority DESC, created_at ASC LIMIT $3`, arg.ProjectID, arg.Status, arg.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.SpecID, &t.SpecVersion, &t.Title, &t.Description, &t.Status, &t.Priority, &t.EstimatedHours, &t.AssignedTo, &t.Confidence, &t.ActionRequired, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, &t)
	}
	return items, rows.Err()
}

func (q *Queries) UpdateTaskStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := q.pool.Exec(ctx, `UPDATE tasks SET status=$2, updated_at=now() WHERE id=$1`, id, status)
	return err
}
