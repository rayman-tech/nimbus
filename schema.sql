CREATE TABLE projects (
  id        UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  name      TEXT        NOT NULL,
  api_key   TEXT        NOT NULL
);

CREATE TABLE services (
  id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id        UUID        NOT NULL,
  project_branch    TEXT        NOT NULL,
  service_name      TEXT        NOT NULL,
  node_ports        INTEGER[]   NULL,
  ingress           TEXT        NULL,
  FOREIGN KEY (project_id) REFERENCES projects(id),
  UNIQUE (project_id, project_branch, name)
);

CREATE TABLE volumes (
  identifier        UUID    PRIMARY KEY,
  volume_name       TEXT    NOT NULL,
  project_id        UUID    NOT NULL,
  project_branch    TEXT    NOT NULL,
  size              INTEGER NOT NULL,
  FOREIGN KEY (project_id) REFERENCES projects(id)
);
