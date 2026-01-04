CREATE TYPE runtime AS ENUM (
  'docker',
  'kubernetes'
);

CREATE TABLE projects (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid (),
  name text NOT NULL
);

CREATE TABLE services (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid (),
  project_id uuid NOT NULL,
  project_branch text NOT NULL,
  service_name text NOT NULL,
  runtime RUNTIME NOT NULL DEFAULT 'kubernetes',
  -- Kubernetes specific
  node_ports integer[] NULL,
  ingress text NULL,
  -- Docker specific
  port_maps text[] NULL,
  container_name text NULL,
  docker_network text NULL,
  FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
  UNIQUE (project_id, project_branch, service_name)
);

CREATE OR REPLACE VIEW docker_services AS
SELECT
  id,
  project_id,
  project_branch,
  service_name,
  runtime,
  port_maps,
  container_name,
  docker_network
FROM
  services
WHERE
  runtime = 'docker' WITH CHECK OPTION;

CREATE OR REPLACE VIEW kubernetes_services AS
SELECT
  id,
  project_id,
  project_branch,
  service_name,
  runtime,
  node_ports,
  ingress
FROM
  services
WHERE
  runtime = 'kubernetes' WITH CHECK OPTION;

CREATE TABLE volumes (
  identifier uuid PRIMARY KEY,
  runtime RUNTIME NOT NULL DEFAULT 'kubernetes',
  volume_name text NOT NULL,
  project_id uuid NOT NULL,
  project_branch text NOT NULL,
  -- Kubernetes
  size integer NOT NULL,
  FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE OR REPLACE VIEW docker_volumes AS
SELECT
  identifier,
  runtime,
  volume_name,
  project_id,
  project_branch
FROM
  volumes
WHERE
  runtime = 'docker' WITH CHECK OPTION;

CREATE OR REPLACE VIEW kubernetes_volumes AS
SELECT
  identifier,
  runtime,
  volume_name,
  project_id,
  project_branch,
  size
FROM
  volumes
WHERE
  runtime = 'kubernetes' WITH CHECK OPTION;

CREATE TABLE users (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid (),
  username text NOT NULL,
  api_key text NOT NULL
);

CREATE TABLE user_projects (
  user_id uuid NOT NULL,
  project_id uuid NOT NULL,
  PRIMARY KEY (user_id, project_id),
  FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
  FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
