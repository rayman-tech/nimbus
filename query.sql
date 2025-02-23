-- name: GetProject :one
SELECT * FROM projects
WHERE id = $1 LIMIT 1;

-- name: ListProjects :many
SELECT * FROM projects
ORDER BY name;

-- name: CreateProject :one
INSERT INTO projects (
  name, api_key
) VALUES (
  $1, $2
)
RETURNING *;

-- name: UpdateProject :one
UPDATE projects
  set name = $2,
  api_key = $3
WHERE id = $1
RETURNING *;

-- name: DeleteProject :exec
DELETE FROM projects
WHERE id = $1;
