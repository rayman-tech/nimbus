CREATE TABLE projects (
  name      TEXT        PRIMARY KEY,
  api_key   TEXT        NOT NULL,
  node_ports INTEGER[]  NULL
);
