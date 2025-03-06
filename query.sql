-- name: GetProject :one
SELECT * FROM projects
WHERE name = $1 LIMIT 1;

-- name: GetProjectByApiKey :one
SELECT * FROM projects
WHERE api_key = $1 LIMIT 1;

-- name: ListProjects :many
SELECT * FROM projects
ORDER BY name;

-- name: CreateProject :one
INSERT INTO projects (
  name, api_key, node_ports
) VALUES (
  $1, $2, NULL
)
RETURNING *;

-- name: UpdateProject :one
UPDATE projects
  SET node_ports = $2
WHERE name = $1
RETURNING *;

-- name: DeleteProject :exec
DELETE FROM projects
WHERE name = $1;
