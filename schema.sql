CREATE TABLE projects (
  name      TEXT        PRIMARY KEY,
  api_key   TEXT        NOT NULL
);

CREATE TABLE services (
  name          TEXT        NOT NULL,
  project_name  TEXT        NOT NULL,
  node_ports    INTEGER[]   NULL,
  ingress       TEXT        NULL,
  FOREIGN KEY (project_name) REFERENCES projects(name),
  PRIMARY KEY (name, project_name)
);

CREATE TABLE volumes (
  identifier    TEXT    PRIMARY KEY,
  volume_name   TEXT    NOT NULL,
  service_name  TEXT    NOT NULL,
  project_name  TEXT    NOT NULL,
  FOREIGN KEY (service_name, project_name) REFERENCES services(name, project_name)
);
