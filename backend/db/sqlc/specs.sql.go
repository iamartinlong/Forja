package sqlc

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type CreateSpecParams struct {
	ID             uuid.UUID   `json:"id"`
	ProjectID      uuid.UUID   `json:"project_id"`
	Layer          string      `json:"layer"`
	Title          string      `json:"title"`
	CurrentVersion pgtype.Text `json:"current_version"`
}

func (q *Queries) CreateSpec(ctx context.Context, arg CreateSpecParams) (*Spec, error) {
	row := q.pool.QueryRow(ctx, `INSERT INTO specs (id, project_id, layer, title, status, current_version) VALUES ($1,$2,$3,$4,'DRAFT',$5) RETURNING id, project_id, layer, title, status, current_version, created_at, updated_at`, arg.ID, arg.ProjectID, arg.Layer, arg.Title, arg.CurrentVersion)
	var s Spec
	err := row.Scan(&s.ID, &s.ProjectID, &s.Layer, &s.Title, &s.Status, &s.CurrentVersion, &s.CreatedAt, &s.UpdatedAt)
	return &s, err
}

func (q *Queries) GetSpec(ctx context.Context, id uuid.UUID) (*Spec, error) {
	row := q.pool.QueryRow(ctx, `SELECT id, project_id, layer, title, status, current_version, created_at, updated_at FROM specs WHERE id=$1`, id)
	var s Spec
	err := row.Scan(&s.ID, &s.ProjectID, &s.Layer, &s.Title, &s.Status, &s.CurrentVersion, &s.CreatedAt, &s.UpdatedAt)
	return &s, err
}

type ListSpecsParams struct {
	ProjectID uuid.UUID   `json:"project_id"`
	Layer     pgtype.Text `json:"layer"`
	Limit     int32       `json:"limit"`
	Offset    int32       `json:"offset"`
}

func (q *Queries) ListSpecs(ctx context.Context, arg ListSpecsParams) ([]*Spec, error) {
	rows, err := q.pool.Query(ctx, `SELECT id, project_id, layer, title, status, current_version, created_at, updated_at FROM specs WHERE project_id=$1 AND ($2::varchar IS NULL OR layer=$2) ORDER BY created_at DESC LIMIT $3 OFFSET $4`, arg.ProjectID, arg.Layer, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*Spec
	for rows.Next() {
		var s Spec
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.Layer, &s.Title, &s.Status, &s.CurrentVersion, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, &s)
	}
	return items, rows.Err()
}

type CreateSpecVersionParams struct {
	ID        uuid.UUID   `json:"id"`
	SpecID    uuid.UUID   `json:"spec_id"`
	Version   string      `json:"version"`
	Content   []byte      `json:"content"`
	Embedding pgtype.Text `json:"embedding"`
	ChangeLog pgtype.Text `json:"change_log"`
	CreatedBy pgtype.UUID `json:"created_by"`
}

func (q *Queries) CreateSpecVersion(ctx context.Context, arg CreateSpecVersionParams) (*SpecVersion, error) {
	row := q.pool.QueryRow(ctx, `INSERT INTO spec_versions (id, spec_id, version, content, embedding, change_log, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, spec_id, version, content, embedding, change_log, created_by, created_at`, arg.ID, arg.SpecID, arg.Version, arg.Content, arg.Embedding, arg.ChangeLog, arg.CreatedBy)
	var s SpecVersion
	err := row.Scan(&s.ID, &s.SpecID, &s.Version, &s.Content, &s.Embedding, &s.ChangeLog, &s.CreatedBy, &s.CreatedAt)
	return &s, err
}
