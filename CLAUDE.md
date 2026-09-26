# Nimbus

## Local build and push
```bash
KO_DOCKER_REPO=docker.prayujt.com/nimbus \
  ko build ./cmd --bare --platform=linux/amd64,linux/arm64 \
  --tags="latest,$(git rev-parse --short HEAD)"
```
