-- name: GetProject :one
SELECT * FROM projects
WHERE id = $1 LIMIT 1;


-- name: CreateProject :one
INSERT INTO projects (
  id, name
) VALUES (
  $1, $2
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
ORDER BY service_name;

-- name: CreateService :one
INSERT INTO services (
  id, project_id, project_branch, service_name, node_ports, ingress
) VALUES (
  $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: DeleteServiceByName :exec
DELETE FROM services
WHERE service_name = $1 AND project_id = $2 AND project_branch = $3;

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

-- name: GetUserByApiKey :one
SELECT * FROM users
WHERE api_key = $1 LIMIT 1;

-- name: GetProjectByName :one
SELECT * FROM projects
WHERE name = $1 LIMIT 1;

-- name: IsUserInProject :one
SELECT EXISTS (
  SELECT 1 FROM user_projects
  WHERE user_id = $1 AND project_id = $2
);

-- name: DeleteUnusedVolumes :exec
DELETE FROM volumes
WHERE project_id = $1 AND project_branch = $2 AND NOT volume_name = ANY($3::text[]);

-- name: GetProjectsByUser :many
SELECT p.* FROM projects p
JOIN user_projects up ON p.id = up.project_id
WHERE up.user_id = $1
ORDER BY p.name;

-- name: GetServicesByUser :many
SELECT s.*, p.name AS project_name FROM services s
JOIN projects p ON s.project_id = p.id
JOIN user_projects up ON up.project_id = p.id
WHERE up.user_id = $1
ORDER BY p.name, s.service_name;

-- name: GetServiceByName :one
SELECT * FROM services
WHERE service_name = $1 AND project_id = $2 AND project_branch = $3
LIMIT 1;

-- name: AddUserToProject :exec
INSERT INTO user_projects (user_id, project_id)
VALUES ($1, $2);
