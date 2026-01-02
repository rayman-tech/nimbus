# nimbus

An open-source cloud-native deployment tool.

## Prerequisites

Before setting up Nimbus, ensure you have the following installed:
- [Kubernetes](https://kubernetes.io/docs/setup/) (either a remote cluster or local installation)
- [Docker](https://docs.docker.com/get-docker/)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Make](https://www.gnu.org/software/make/)
- [Go](https://go.dev/doc/install)

For local development, we recommend using [Minikube](https://minikube.sigs.k8s.io/docs/) to set up a local Kubernetes cluster. Ensure your Kubernetes configuration is available at `~/.kube/config`.

## Server Installation

To deploy Nimbus on an existing Kubernetes cluster:

1. Create the required namespace:
   ```sh
   kubectl create namespace nimbus
   ```
2. Apply the Kubernetes configurations:
   ```sh
   kubectl apply -f kubernetes/
   ```

### Production Considerations

In a production environment, the Nimbus server will have elevated admin permissions over your entire Kubernetes cluster. These permissions are specified in the `permissions.yaml` file through the ServiceAccount configuration. Ensure you review and understand the security implications before deployment.

Additionally, to run Nimbus in production, you must set the environment variable `ENVIRONMENT=production`. If this variable is not specified, it defaults to `development`.

### Persistent Storage Requirement

For hosting a Nimbus server, you need some kind of NFS persistent volume provisioner installed. The recommended provisioner is:

[NFS Subdir External Provisioner](https://github.com/kubernetes-sigs/nfs-subdir-external-provisioner)

Ensure that your cluster has a properly configured NFS provisioner before deploying Nimbus to prevent storage-related issues.

You also need to set the environment variable `NIMBUS_STORAGE_CLASS` with the name of the storage class you have configured with the provisioner. By default, this is set to `nfs-client`.

To restrict deployments to only the `main` or `master` branches for a project, add `allowBranchPreviews: false` to your project's `nimbus.yaml`. When disabled, deploy requests from any other branch will be rejected.

## Local Development

For local development, you can run Nimbus either directly or using Docker Compose.

### Running with Docker Compose (Recommended)

This method runs the server with hot-reloading enabled and includes a PostgreSQL database:

1. Copy the `.env.example` files and set the required variables:
   ```sh
   cp .env.example .env
   cp .env.database.example .env.database
   ```
2. Ensure Kubernetes is running locally (Minikube is recommended):
   ```sh
   minikube start
   ```
3. Start the development environment:
   ```sh
   make docker-up
   ```
4. To stop the development environment:
   ```sh
   make docker-down
   ```
5. To stop and remove volumes:
   ```sh
   make docker-down-volumes
   ```

You may view the logs of the container as such:

```sh
docker logs nimbus-api
```

### Running Directly

Alternatively, you can run the server directly without Docker:

1. Copy the `.env.example` file to `.env` and set the required environment variables:
   ```sh
   cp .env.example .env
   ```
2. Ensure Kubernetes is running locally (Minikube is recommended):
   ```sh
   minikube start
   ```
3. Run the Nimbus server locally:
   ```sh
   make server
   ```

### Development Tools

The following tools are included as development dependencies:

- **[golangci-lint](https://golangci-lint.run/)** - Go linter aggregator for running multiple linters
- **[pg_format](https://github.com/darold/pgFormatter)** - PostgreSQL SQL syntax formatter
- **[air](https://github.com/cosmtrek/air)** - Hot-reloading tool for Go applications

These tools are automatically configured in the project's Makefile for linting, formatting, and development tasks.

## Deployment

A `nimbus.yaml` file needs to be present in your repository to deploy to Nimbus.
This file defines the services that will be deployed, as well as the networking and environment configurations.
There are pre-defined templates for services, such as databases and Redis, to make configuration easier. A sample `nimbus.yaml` file is available [here](https://github.com/rayman-tech/nimbus-action/blob/main/nimbus.yaml).

Deployment can be done through our [GitHub action](https://github.com/rayman-tech/nimbus-action), or through the local CLI, which is used for managing project state.

## CLI Installation

Install the Nimbus CLI locally with:

```sh
sudo make install
```

This command builds the `nimbus` binary and copies it to `/usr/local/bin`,
allowing you to run `nimbus` from any directory.

## CLI Usage

After installing the CLI, you can deploy your application easily:

```sh
nimbus deploy
```

Flags:

- `-H`, `--host` – Nimbus server address. Defaults to the `NIMBUS_HOST` environment variable or `http://localhost:8080`.
- `-f`, `--file` – Path to the `nimbus.yaml` file. Defaults to `./nimbus.yaml`.
- `-a`, `--apikey` – API key used for authentication. Defaults to the `NIMBUS_API_KEY` environment variable.

The client CLI exposes several subcommands:

- `nimbus deploy` – deploy a project using a `nimbus.yaml` file.
- `nimbus projects` – manage projects (`create`, `list`, `delete`).
- `nimbus services` – inspect services (`list`, `get`, `logs`).
- `nimbus secrets` – manage project secrets (`list`, `edit`).
- `nimbus branch delete` – remove a branch and its resources.

Running `nimbus server` will start the server locally.

## Contributing

We welcome contributions! Feel free to submit issues and pull requests to improve Nimbus.
