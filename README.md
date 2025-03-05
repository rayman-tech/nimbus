# nimbus

An open-source cloud-native deployment tool.

## Prerequisites

Before setting up Nimbus, ensure you have the following installed:
- [Kubernetes](https://kubernetes.io/docs/setup/) (either a remote cluster or local installation)
- [Docker](https://docs.docker.com/get-docker/)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Make](https://www.gnu.org/software/make/)

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

## Local Development

For local development, follow these steps:

1. Ensure Kubernetes is running locally (Minikube is recommended):
   ```sh
   minikube start
   ```
2. Run the Nimbus server locally:
   ```sh
   make server
   ```

## Contributing

We welcome contributions! Feel free to submit issues and pull requests to improve Nimbus.

## License

Nimbus is open-source and licensed under the MIT License.
