package sqlc

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type Project struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Description pgtype.Text `json:"description"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Spec struct {
	ID             uuid.UUID `json:"id"`
	ProjectID      uuid.UUID `json:"project_id"`
	Layer          string    `json:"layer"`
	Title          string    `json:"title"`
	Status         string    `json:"status"`
	CurrentVersion pgtype.Text `json:"current_version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type SpecVersion struct {
	ID        uuid.UUID `json:"id"`
	SpecID    uuid.UUID `json:"spec_id"`
	Version   string    `json:"version"`
	Content   []byte    `json:"content"`
	Embedding pgtype.Text `json:"embedding"`
	ChangeLog pgtype.Text `json:"change_log"`
	CreatedBy pgtype.UUID `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

type Task struct {
	ID             uuid.UUID   `json:"id"`
	ProjectID      uuid.UUID   `json:"project_id"`
	SpecID         pgtype.UUID `json:"spec_id"`
	SpecVersion    pgtype.Text `json:"spec_version"`
	Title          string      `json:"title"`
	Description    pgtype.Text `json:"description"`
	Status         string      `json:"status"`
	Priority       int32       `json:"priority"`
	EstimatedHours pgtype.Numeric `json:"estimated_hours"`
	AssignedTo     pgtype.UUID `json:"assigned_to"`
	Confidence     pgtype.Float8 `json:"confidence"`
	ActionRequired []byte      `json:"action_required"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

type AuditLog struct {
	ID         uuid.UUID `json:"id"`
	ProjectID  uuid.UUID `json:"project_id"`
	Actor      string    `json:"actor"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type"`
	TargetID   pgtype.UUID `json:"target_id"`
	Detail     []byte    `json:"detail"`
	CreatedAt  time.Time `json:"created_at"`
}
