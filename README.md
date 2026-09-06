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

### Monitoring

To have Prometheus scrape a service, add a `monitoring` block to it. Only the
port is required; the path defaults to `/metrics`:

```yaml
services:
  - name: api
    image: docker.prayujt.com/api:latest
    network:
      ports: [8080]
    monitoring:
      port: 8080      # container port exposing metrics
      path: /metrics  # optional, defaults to /metrics
```

Nimbus adds the standard `prometheus.io/scrape`, `prometheus.io/port`, and
`prometheus.io/path` annotations to the service's pods, which a Prometheus
instance using the `kubernetes-pods` scrape job will discover automatically.
Omit the block to disable scraping.

Nimbus also exposes its own Prometheus metrics at `GET /metrics` (HTTP request
counts/latency, deploy outcomes, Kubernetes API operation timings, and Go
runtime/process metrics). This endpoint is unauthenticated and bypasses the API
validator.

### Envoy Gateway routing

Public `http` services now create Gateway API resources instead of NGINX
Ingress objects. The existing `ingress:` YAML field, API/DB hostname field,
and `ingress-host` environment references retain their names for compatibility.
Custom domains on main/master and previously assigned preview hostnames are
preserved. The GitHub action continues to submit the same deployment request.

Nimbus creates an HTTPS listener on the configured shared Gateway, an
HTTPRoute (or GRPCRoute for `features: [grpc]`), an HTTP-to-HTTPS redirect,
a Certificate, and the required policies/ReferenceGrant. Resource and listener
names identify their service; only long names get a short collision-resistant
suffix. Other applications' Gateway listeners are preserved during updates and
cleanup. Existing preview hostname strings are not renamed.

Before deploying this Nimbus version:

- Install Envoy Gateway **1.9.1** with Gateway API **1.6.1**, including its
  extension CRDs. Enable `config.envoyGateway.extensionApis.enableLua=true`
  in the controller chart for the request-size policies, then roll out the
  controller after configuration changes.
- Configure `ENVOY_GATEWAY_NAME` (default `edge`),
  `ENVOY_GATEWAY_NAMESPACE` (default `envoy-gateway-system`), and
  `ENVOY_HTTP_LISTENER` (default `http`). The Gateway needs a port-80 HTTP
  listener admitting routes from Nimbus application namespaces. Its public
  Service/router entrypoint must already exist; Nimbus does not replace it.
- Enable Gateway API in cert-manager and configure `letsencrypt-prod` (or the
  service's selected ClusterIssuer) for Gateway HTTP-01 on that listener.
  Certificate issuance must complete before a deployment is reported ready.
- For SPA or legacy external-auth services, set `NIMBUS_ROUTE_HELPER_IMAGE`
  to a **digest-pinned image built from this Nimbus version**, available for
  the application's node architectures. For example,
  `registry.example.com/nimbus@sha256:<actual-manifest-digest>`.
  This is not the separate kubeconfigs authentication helper image.
- Nimbus needs access to its application resources and the configured shared
  Gateway/ClientTrafficPolicies. The existing installation example grants
  cluster-admin; this migration does not broaden that binding.

`features: [grpc]` creates a GRPCRoute and marks the backend Service port
`kubernetes.io/h2c`. Ordinary HTTP routes retain the former 1 MiB request limit
and 5s connect / 60s idle timeouts. gRPC and WebSocket upgrades are not buffered
by the HTTP body-limit filter.

`features: [spa]` uses a small per-service helper to preserve the former
upstream-404 → `/index.html` fallback. External authentication uses the same
helper's separate auth port. Helpers run the `nimbus route-helper` subcommand,
with two replicas, read-only configuration, no service-account token, and no
privileges. Hostnames and upstreams are fixed by each service's ConfigMap;
requests cannot select an arbitrary upstream. Successful SPA responses stream.

### Envoy annotations

Use `envoy.nimbus.dev/*` settings in `nimbus.yaml`. These are Nimbus configuration
keys: Nimbus validates them and generates Gateway API routes and Envoy policies.
They are not native Envoy Gateway annotations, and copying them onto an arbitrary
Kubernetes resource does not configure Envoy. Unknown keys under this prefix are
rejected.

For example, external authentication:

```yaml
services:
  - name: web
    template: http
    public: true
    ingress: app.example.com
    image: registry.example.com/web:latest
    network:
      ports: [8080]
    annotations:
      envoy.nimbus.dev/auth-url: "https://idp.example.com/sessions/whoami"
      envoy.nimbus.dev/auth-signin: "https://login.example.com/start?rd=$scheme://$host$request_uri"
```

The adapter accepts a successful 2xx session check, preserves 403 denial, and
turns a 401 into the configured login redirect (or returns 401 when no sign-in
URL is supplied). Network errors, missing required identity headers, and
unexpected auth responses deny access. Envoy removes incoming identity headers
before auth; only configured headers returned by the auth service reach the app.
This preserves existing session checks; it does not add user/role management.

All keys below use the `envoy.nimbus.dev/` prefix:

| Key | Values / behavior |
| --- | --- |
| `backend-protocol` | `http` or `h2c`; `h2c` selects a GRPCRoute and HTTP/2 backend. `features: [grpc]` remains shorthand. Backend TLS is not implemented. |
| `connect-timeout` | Positive duration, default `5s`. |
| `stream-idle-timeout` | Duration with no stream activity, default `60s`; `0s` disables it. |
| `request-timeout` | Entire upstream response timeout, default `0s` (disabled). |
| `max-stream-duration` | Maximum stream duration, default `0s` (unlimited). |
| `grpc-service`, `grpc-method` | Optional exact protobuf service/method matches on a gRPC route; omitted means all services/methods. |
| `grpc-retry-count`, `grpc-retry-on` | Explicit opt-in retries; both required. Count 1–10. Triggers listed below. |
| `grpc-per-retry-timeout` | Optional positive duration; requires retry count and triggers. |
| `auth-url`, `auth-signin`, `auth-response-headers` | Session check URL, optional login redirect, comma-separated authenticated identity headers. |
| `enable-cors` | `true` or `false`; default disabled. |
| `cors-allow-origin`, `cors-allow-methods`, `cors-allow-headers`, `cors-expose-headers` | Comma-separated CORS values. |
| `cors-allow-credentials`, `cors-max-age` | Boolean and cache lifetime in seconds. |
| `body-size` | Request limit, default `1m`; integer bytes or `k`/`m`/`g` suffix; `0` unlimited. Not buffered for gRPC. |
| `spa` | `true` enables the helper fallback; `features: [spa]` remains shorthand. Cannot combine with gRPC. |
| `cluster-issuer` | Certificate ClusterIssuer, default `letsencrypt-prod`. |
| `ssl-redirect` | `true`; public routes always redirect HTTP to HTTPS. |

Timeouts accept Gateway API duration syntax such as `250ms`, `5s`, or `5m`, up
to 24 hours. Retry triggers are `cancelled`, `deadline-exceeded`, `internal`,
`resource-exhausted`, `unavailable`, `connect-failure`, `refused-stream`, and
`reset-before-request`. Retries are absent by default; enable them only for RPCs
that are safe to repeat. Envoy's gRPC status retries apply to response headers,
not status codes delivered in trailers, and cannot replay an established stream.

```yaml
services:
  - name: grpc-api
    template: http
    public: true
    image: registry.example.com/grpc-api:latest
    features: [grpc]
    network:
      ports: [50051]
    annotations:
      envoy.nimbus.dev/backend-protocol: "h2c"
      envoy.nimbus.dev/connect-timeout: "5s"
      envoy.nimbus.dev/stream-idle-timeout: "300s"
      envoy.nimbus.dev/request-timeout: "0s"
      # Optional restriction to a single protobuf service / RPC:
      # envoy.nimbus.dev/grpc-service: "example.v1.Catalog"
      # envoy.nimbus.dev/grpc-method: "GetItem"
      # Optional retries for an RPC that is safe to repeat:
      # envoy.nimbus.dev/grpc-retry-count: "2"
      # envoy.nimbus.dev/grpc-retry-on: "unavailable"
      # envoy.nimbus.dev/grpc-per-retry-timeout: "5s"
```

This annotation interface supports a single service/method match and retry policy
per Nimbus service. Multiple match rules and weighted backends would need a future
structured configuration interface; arbitrary policy YAML is not accepted.

### Deprecated NGINX aliases

Previously supported `nginx.ingress.kubernetes.io/*` keys remain deprecated input
aliases. Prefer the Envoy keys above for new configuration. Most keep the same
suffix; `proxy-body-size` becomes `body-size`, `proxy-connect-timeout` becomes
`connect-timeout`, and `proxy-read-timeout` / `proxy-send-timeout` become
`stream-idle-timeout`. Old timeout values are integer seconds; new ones include
units. `backend-protocol: GRPC` becomes `backend-protocol: h2c`, and the exact old
SPA `configuration-snippet` becomes `spa: "true"`. The existing
`cert-manager.io/cluster-issuer` also remains an alias.

Supplying conflicting new and old settings is a validation error. Matching aliases
are accepted (duration values are compared by elapsed time). An explicit HTTP
backend conflicts with `features: [grpc]`; `spa: "false"` conflicts with the SPA
feature/snippet. Unknown NGINX annotations and arbitrary snippets are rejected.
CORS origin settings alone remain inert unless `enable-cors: "true"` is set.
CORS and authentication share a SecurityPolicy when both apply. Non-NGINX metadata
annotations, including the explicit Envoy inputs, remain visible on the HTTPS route.

### Redeploy and cleanup

Redeploy each existing service to migrate its stored hostname. Nimbus installs
helper/policy resources before attaching the route, waits for fresh route and
policy acceptance, certificate readiness, and helper availability, then deletes
only that service's old `<service>-ingress`. Existing TLS Secrets and private-key
settings are preserved; old Ingress certificate ownership is detached. A failed
readiness check reports deployment failure and retains the legacy Ingress;
it does not provide a transactional rollback of all application changes.
`ENVOY_READY_TIMEOUT_SECONDS` defaults to 180 per service. Set the proxy in
front of the Nimbus API to allow enough idle time for the deployment request
(the sum of sequential service waits); the action does not stream progress.

Making a service private, changing it to a non-HTTP template, or deleting a
service/preview removes its owned routes, shared listener, policies, and helper.
Cleanup errors retain the DB record/hostname for retry. Certificates/keys are
retained for redeployment; deleting the branch namespace removes its namespaced
resources. Do not delete namespaces outside Nimbus and expect shared Gateway
listeners to be garbage-collected automatically.

The shared Gateway has a 64-listener limit. Capacity and hostname conflicts
produce errors; Nimbus does not overwrite another application's listener.
Keep the configured Gateway stable while services are deployed, and avoid
reapplying a static Gateway manifest that discards Nimbus-added listeners.

This change does not deploy Envoy, switch router ports, or migrate every stored
service at Nimbus startup. Test browser login, uploads, streaming, and gRPC in
your deployment environment before retiring a still-active old controller.

### Routing validation

```sh
go test -race ./...
go vet ./...
NIMBUS_ROUTE_FIXTURES=/tmp/nimbus-routes.json go test ./internal/kubernetes -run TestExportRouteFixtures
python3 scripts/validate-routes.py /tmp/nimbus-routes.json /path/to/envoy-v1.9.1-install.yaml --egctl /path/to/egctl
```

The offline validator uses the pinned Envoy CRD schemas and synthetic
certificates/services for xDS translation; it never contacts a cluster. Unit
tests cover the helper, hostname compatibility, auth/CORS preservation, gRPC,
readiness-gated ingress retirement, listener isolation, and cleanup ownership.


## API Documentation

The Nimbus API is documented using the OpenAPI 3.0.1 specification, available in `docs/api.yaml`. This documentation provides detailed information about all available endpoints, request/response schemas, and authentication requirements.

### Viewing the Documentation

A Swagger UI server is included in both the Docker Compose and Kubernetes configurations for interactive API exploration:

- **Docker Compose**: When running `make docker-up`, Swagger UI is automatically available at `http://localhost:8080/docs/`
- **Kubernetes**: The Swagger UI is deployed as part of the Kubernetes manifests in `kubernetes/` and can be accessed through the configured ingress or service

The Swagger UI provides an interactive interface to explore the API, view schemas, and test endpoints directly from your browser.

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

## Native Authentik forward auth

For HTTP applications, select direct Authentik integration explicitly:

```yaml
annotations:
  envoy.nimbus.dev/auth-provider: authentik
  # Optional; these are the defaults:
  envoy.nimbus.dev/authentik-service: authentik-server
  envoy.nimbus.dev/authentik-namespace: authentik
  envoy.nimbus.dev/authentik-port: "80"
```

Nimbus emits a fail-closed SecurityPolicy calling
/outpost.goauthentik.io/auth/envoy and an unauthenticated callback HTTPRoute for
/outpost.goauthentik.io only. The normal application route stays protected. The
outpost returns login redirects directly; no auth-signin adapter is required.
Incoming identity headers are removed before authorization. SPA helpers remain
independent; an auth-only application no longer needs a helper image or deployment.

Configure a single-application forward-auth provider in Authentik for the actual
hostname and assign it to a healthy outpost before deployment. Preview hostnames
need their own provider; unknown hosts fail closed. In the Authentik namespace,
an administrator must create a ReferenceGrant admitting SecurityPolicy and HTTPRoute
from the application's namespace to the exact outpost Service, plus any necessary
NetworkPolicy allowing the Envoy proxy. Nimbus does not grant itself cross-namespace
permissions or configure Authentik users/providers. Do not attach auth to the callback
route, and do not use a Gateway-wide auth policy that also catches callbacks.

This mode cannot be combined with auth-url/auth-signin (including deprecated NGINX
aliases), and browser forward-auth on GRPCRoute is rejected. Existing gRPC routing,
legacy auth and SPA behavior are unchanged. Removing the setting or deleting the
service also removes its owned callback route. The feature is inactive until a
service explicitly selects it.
