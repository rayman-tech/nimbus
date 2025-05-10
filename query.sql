-- name: GetProject :one
SELECT * FROM projects
WHERE id = $1 LIMIT 1;

-- name: GetProjectByApiKey :one
SELECT * FROM projects
WHERE api_key = $1 LIMIT 1;

-- name: CreateProject :one
INSERT INTO projects (
  id, name, api_key
) VALUES (
  $1, $2, $3
)
RETURNING *;

-- name: DeleteProject :exec
DELETE FROM projects
WHERE id = $1;


-- name: GetService :one
SELECT * FROM services
WHERE id = $1 LIMIT 1;

-- name: GetServicesByProject :many
SELECT * FROM services
WHERE project_id = $1 AND project_branch = $2
ORDER BY name;

-- name: CreateService :one
INSERT INTO services (
  id, project_id, project_branch, name, node_ports, ingress
) VALUES (
  $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: DeleteServiceByName :exec
DELETE FROM services
WHERE name = $1 AND project_id = $2 AND project_branch = $3;

-- name: DeleteServiceById :exec
DELETE FROM services
WHERE id = $1;

-- name: SetServiceNodePorts :exec
UPDATE services SET
  node_ports = $2
WHERE id = $1 RETURNING *;

-- name: SetServiceIngress :exec
UPDATE services SET
  ingress = $2
WHERE id = $1 RETURNING *;


-- name: GetVolumeIdentifier :one
SELECT identifier FROM volumes
WHERE volume_name = $1 AND project_id = $2 AND project_branch = $3;

-- name: CreateVolume :one
INSERT INTO volumes (
  identifier, volume_name, project_id, project_branch, size
) VALUES (
  $1, $2, $3, $4, $5
) RETURNING *;

-- name: GetUnusedVolumeIdentifiers :many
SELECT identifier FROM volumes
WHERE project_id = $1 AND project_branch = $2 AND NOT volume_name = ANY($3::text[]);

-- name: DeleteUnusedVolumes :exec
DELETE FROM volumes
WHERE project_id = $1 AND project_branch = $2 AND NOT volume_name = ANY($3::text[]);
