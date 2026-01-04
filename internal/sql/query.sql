-- name: GetProject :one
SELECT
  *
FROM
  projects
WHERE
  id = $1
LIMIT 1;

-- name: CreateProject :one
INSERT INTO projects (id, name)
  VALUES ($1, $2)
RETURNING
  *;

-- name: DeleteProject :exec
DELETE FROM projects
WHERE id = $1;

-- name: GetKubernetesServicesByProject :many
SELECT
  *
FROM
  kubernetes_services
WHERE
  project_id = $1
  AND project_branch = $2
ORDER BY
  service_name;

-- name: CreateKubernetesService :one
INSERT INTO kubernetes_services (id, project_id, project_branch, service_name, node_ports, ingress)
  VALUES ($1, $2, $3, $4, $5, $6)
RETURNING
  *;

-- name: DeleteKubernetesServiceByName :exec
DELETE FROM kubernetes_services
WHERE service_name = $1
  AND project_id = $2
  AND project_branch = $3;

-- name: DeleteKubernetesServiceById :exec
DELETE FROM kubernetes_services
WHERE id = $1;

-- name: SetKubernetesServiceNodePorts :exec
UPDATE
  kubernetes_services
SET
  node_ports = $2
WHERE
  id = $1
RETURNING
  *;

-- name: SetKubernetesServiceIngress :exec
UPDATE
  kubernetes_services
SET
  ingress = $2
WHERE
  id = $1
RETURNING
  *;

-- name: GetKubernetesVolumeIdentifier :one
SELECT
  identifier
FROM
  kubernetes_volumes
WHERE
  volume_name = $1
  AND project_id = $2
  AND project_branch = $3;

-- name: CreateKubernetesVolume :one
INSERT INTO volumes (identifier, volume_name, project_id, project_branch, size)
  VALUES ($1, $2, $3, $4, $5)
RETURNING
  *;

-- name: GetUnusedKubernetesVolumeIdentifiers :many
SELECT
  identifier
FROM
  kubernetes_volumes
WHERE
  project_id = $1
  AND project_branch = $2
  AND NOT volume_name = ANY (@exclude_volumes::text[]);

-- name: GetUserByApiKey :one
SELECT
  *
FROM
  users
WHERE
  api_key = $1
LIMIT 1;

-- name: GetApiKeyExistance :one
SELECT
  EXISTS (
    SELECT
      1
    FROM
      users
    WHERE
      api_key = $1);

-- name: GetProjectByName :one
SELECT
  *
FROM
  projects
WHERE
  name = $1
LIMIT 1;

-- name: GetProjectById :one
SELECT
  id,
  name
FROM
  projects
WHERE
  id = $1;

-- name: IsUserInProject :one
SELECT
  EXISTS (
    SELECT
      1
    FROM
      user_projects
    WHERE
      user_id = $1
      AND project_id = $2);

-- name: DeleteUnusedKubernetesVolumes :exec
DELETE FROM kubernetes_volumes
WHERE project_id = $1
  AND project_branch = $2
  AND NOT volume_name = ANY (@exclude_volumes::text[]);

-- name: GetProjectsByUser :many
SELECT
  p.*
FROM
  projects p
  JOIN user_projects up ON p.id = up.project_id
WHERE
  up.user_id = $1
ORDER BY
  p.name;

-- name: GetKubernetesServicesByUser :many
SELECT
  s.*,
  p.name AS project_name
FROM
  kubernetes_services s
  JOIN projects p ON s.project_id = p.id
  JOIN user_projects up ON up.project_id = p.id
WHERE
  up.user_id = $1
ORDER BY
  p.name,
  s.service_name;

-- name: GetKubernetesServiceByName :one
SELECT
  *
FROM
  kubernetes_services
WHERE
  service_name = $1
  AND project_id = $2
  AND project_branch = $3
LIMIT 1;

-- name: AddUserToProject :exec
INSERT INTO user_projects (user_id, project_id)
  VALUES ($1, $2);

-- name: GetKubernetesProjectBranches :many
SELECT
  project_branch
FROM
  kubernetes_services s
WHERE
  s.project_id = $1
UNION
SELECT
  project_branch
FROM
  volumes v
WHERE
  v.project_id = $1;

-- name: CheckProjectsTableExists :one
SELECT
  EXISTS (
    SELECT
      1
    FROM
      information_schema.tables
    WHERE
      table_schema = 'public'
      AND table_name = 'projects');
