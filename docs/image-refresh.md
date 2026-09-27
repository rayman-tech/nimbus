# Following published images

Image refresh is opt-in for each service. A service without
`imageAutoRefresh: true` is never checked against the registry by the worker.
The existing commit-tag deployment behavior remains the default.

## Service configuration

Add the flag to the service in your existing Nimbus YAML, keeping its environment,
authentication, hostname, and volumes:

```yaml
app: planewise-finances
services:
  - name: server
    template: http
    public: true
    image: docker.prayujt.com/beancount:latest
    imageAutoRefresh: true
    network:
      ports: [8080]
    readinessProbe:
      httpGet:
        path: /healthz
        port: 8080
      periodSeconds: 5
```

This is a configuration fragment; retain the existing ledger volume and OIDC
environment when applying it. Submit the complete configuration using the Nimbus
CLI/API or the existing action. A commit sent by the action does not replace the
followed tag. Database templates (`postgres` and `redis`) cannot opt in; use an
explicit image instead. A digest is fixed and cannot be followed.

The Nimbus server environment variable `NIMBUS_IMAGE_REFRESH_INTERVAL` controls
the interval for all opted-in services. It defaults to `5s` and accepts positive
Go durations, for example `30s` or `1m`. It does not enable refresh on any service.

On the initial deploy, Nimbus resolves the tag and deploys its exact digest.
Registry lookup failure rejects that deploy before changing existing workloads.
Thereafter, the worker checks manifest metadata, sharing lookups between services
following the same tag. It does not download image layers. Multi-platform images
are pinned to their top-level index digest so each node selects its own platform.
Promote a followed tag only after the publisher's tests pass.

## Nimbus registry credentials

Public images need no new secret. For a private registry, the Nimbus **server**
needs read-only registry credentials to inspect manifests. GitHub Actions login
credentials and node image-pull credentials are separate from this access.

Create a Docker `config.json` containing an `auths` entry for the registry using
a read-only account. It must contain usable credentials; a reference to a local
credential-helper executable will not work in the Nimbus container.

Create the Kubernetes Secret from that file without putting its contents in Git:

```sh
kubectl -n nimbus create secret generic nimbus-registry-auth \
  --from-file=config.json=/path/to/registry-read-config.json
```

The example [Nimbus Deployment](../kubernetes/nimbus.yaml) mounts that Secret
read-only at `/etc/nimbus/registry` and sets `DOCKER_CONFIG` to that directory.
If managing the Deployment elsewhere, copy its `registry-auth` volume/mount and
`DOCKER_CONFIG` environment entry. The Secret is optional so existing installations
and public-image use continue to work. Missing or invalid credentials for a
private image produce a lookup error. Secret volume rotations are read on later
lookups without a Nimbus restart; failed lookups may be in backoff for up to five
minutes (or the configured polling interval, if longer).

The workload nodes must also be able to pull the image, just as before. This
Secret mount does not automatically configure image pulling in application
namespaces. Existing node registry credentials or service-account pull secrets
can continue to provide that access.

## Status and failures

`GET /services` and `GET /services/{name}` include `image_refresh` for opted-in
services: followed `source`, desired `image`, rollout `state`, and, when available,
`previous_image`, `updated_at`, and `last_error`. `commit_hash` remains the commit
that submitted the configuration, not the image version discovered later.

The desired image can differ from what a still-running old pod uses during a
rollout. States are `ready`, `updating`, `failed`, and `paused`. Configure a
`readinessProbe` so application readiness participates in rollout completion.
The worker waits for the current rollout before applying another image. A failed
rollout requires an operator to fix or roll back the deployment; there is no
automatic rollback. Registry failures leave the existing image untouched and
retry with exponential backoff. An unchanged image causes no Kubernetes write.

Tracking metadata is stored on the Kubernetes Deployment. It survives Nimbus
restarts and disappears with the service/branch. Concurrent configuration changes
or deletion take precedence over stale polling results. Nimbus uses its existing
Kubernetes permissions; no database migration or additional permission is needed
with the repository's cluster role.

Volume-backed services retain Nimbus's `Recreate` strategy, so an update briefly
stops the old container before starting the replacement. For Beancount, a server
restart also ends its in-memory login sessions.

## Disable or roll back

Set `imageAutoRefresh: false` (or remove it), set `image` to the desired immutable
`repository@sha256:...` reference, and submit the full configuration again.
Explicit digests are preserved even when the action submits a commit hash.
This removes tracking metadata and stops polling for the service. Pausing a
Kubernetes Deployment also suspends its image checks.

Re-enable the flag with a tag when ready to follow new releases again. Merely
running `kubectl rollout undo` while tracking is enabled can be undone by the next
successful poll; disable or pause tracking first.
