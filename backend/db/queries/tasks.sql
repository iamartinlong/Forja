-- name: CreateTask :one
INSERT INTO tasks (id, project_id, spec_id, title, description, status, priority, estimated_hours, assigned_to)
VALUES ($1, $2, $3, $4, $5, 'PENDING', $6, $7, $8) RETURNING *;

-- name: GetTask :one
SELECT * FROM tasks WHERE id = $1;

-- name: ListTasks :many
SELECT * FROM tasks WHERE project_id = $1 AND ($2::varchar IS NULL OR status = $2) ORDER BY priority DESC, created_at ASC LIMIT $3;

-- name: UpdateTaskStatus :exec
UPDATE tasks SET status = $2, updated_at = now() WHERE id = $1;

-- name: UpdateTaskPriority :exec
UPDATE tasks SET priority = $2, updated_at = now() WHERE id = $1;

-- name: ListTaskDependencies :many
SELECT depends_on FROM task_dependencies WHERE task_id = $1;

-- name: CreateTaskDependency :exec
INSERT INTO task_dependencies (task_id, depends_on) VALUES ($1, $2);

-- name: GetDagNodes :many
SELECT id, title, status FROM tasks WHERE project_id = $1;

-- name: GetDagEdges :many
SELECT task_id, depends_on FROM task_dependencies WHERE task_id IN (SELECT id FROM tasks WHERE project_id = $1);
