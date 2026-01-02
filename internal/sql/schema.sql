CREATE TABLE projects (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid (),
  name text NOT NULL
);

CREATE TABLE services (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid (),
  project_id uuid NOT NULL,
  project_branch text NOT NULL,
  service_name text NOT NULL,
  node_ports integer[] NULL,
  ingress text NULL,
  FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
  UNIQUE (project_id, project_branch, service_name)
);

CREATE TABLE volumes (
  identifier uuid PRIMARY KEY,
  volume_name text NOT NULL,
  project_id uuid NOT NULL,
  project_branch text NOT NULL,
  size integer NOT NULL,
  FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);

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
