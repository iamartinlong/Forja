-- name: CreateSpec :one
INSERT INTO specs (id, project_id, layer, title, status, current_version)
VALUES ($1, $2, $3, $4, 'DRAFT', $5) RETURNING *;

-- name: GetSpec :one
SELECT * FROM specs WHERE id = $1;

-- name: ListSpecs :many
SELECT * FROM specs WHERE project_id = $1 AND ($2::varchar IS NULL OR layer = $2) ORDER BY created_at DESC LIMIT $3 OFFSET $4;

-- name: CreateSpecVersion :one
INSERT INTO spec_versions (id, spec_id, version, content, embedding, change_log, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: GetSpecVersion :one
SELECT * FROM spec_versions WHERE spec_id = $1 AND version = $2;

-- name: ListSpecVersions :many
SELECT * FROM spec_versions WHERE spec_id = $1 ORDER BY created_at DESC;
