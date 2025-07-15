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

## Deployment

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

## Local Development

For local development, follow these steps:

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

## Service Configuration

Each service defined in your deployment file accepts a `public` flag. When set
to `true`, Nimbus exposes the service publicly via a NodePort or Ingress.
Without this flag, services are created as `ClusterIP` and remain internal only.

Services can also specify an `arch` field with either `amd64` or `arm64` to
target a specific node architecture. Nimbus applies a matching node affinity so
pods only run on nodes with the selected architecture.

## Contributing

We welcome contributions! Feel free to submit issues and pull requests to improve Nimbus.

## CLI Usage

After building the project, you can deploy your application using the CLI:

```sh
nimbus deploy -H http://localhost:8080 -f ./nimbus.yaml -a <API_KEY>
```

Flags:

- `-H`, `--host` – Nimbus server address. Defaults to the `NIMBUS_HOST` environment variable or `http://localhost:8080`.
- `-f`, `--file` – Path to the `nimbus.yaml` file. Defaults to `./nimbus.yaml`.
- `-a`, `--apikey` – API key used for authentication. Defaults to the `NIMBUS_API_KEY` environment variable.

Running `nimbus server` will start the server locally.
