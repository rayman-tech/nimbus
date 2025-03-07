-- name: GetProject :one
SELECT * FROM projects
WHERE name = $1 LIMIT 1;

-- name: GetProjectByApiKey :one
SELECT * FROM projects
WHERE projects.api_key = $1 LIMIT 1;

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

-- name: DeleteProject :exec
DELETE FROM projects
WHERE name = $1;


-- name: GetService :one
SELECT * FROM services
WHERE name = $1 LIMIT 1;

-- name: GetServicesByProject :many
SELECT * FROM services
WHERE project_name = $1;

-- name: ListServices :many
SELECT * FROM services
WHERE project_name = $1
ORDER BY name;

-- name: CreateService :one
INSERT INTO services (
  name, project_name, node_ports, ingress
) VALUES (
  $1, $2, $3, $4
)
RETURNING *;

-- name: DeleteService :exec
DELETE FROM services
WHERE name = $1 AND project_name = $2;

-- name: SetServiceNodePorts :exec
UPDATE services SET
  node_ports = $3
WHERE name = $1 AND project_name = $2 RETURNING *;

-- name: SetServiceIngress :exec
UPDATE services SET
  ingress = $3
WHERE name = $1 AND project_name = $2 RETURNING *;


-- name: GetVolumeIdentifier :one
SELECT identifier FROM volumes
WHERE volume_name = $1 AND project_name = $2 AND service_name = $3 LIMIT 1;

-- name: CreateVolume :one
INSERT INTO volumes (
  volume_name, project_name, service_name, identifier
) VALUES (
  $1, $2, $3, $4
) RETURNING *;

-- name: GetUnusedVolumeIdentifiers :many
SELECT identifier FROM volumes
WHERE project_name = $1 AND service_name = $2 AND volume_name NOT IN (sqlc.slice(volume_names));

-- name: DeleteUnusedVolumes :exec
DELETE FROM volumes
WHERE project_name = $1 AND service_name = $2 AND volume_name NOT IN (sqlc.slice(volume_names));
