# Nimbus

## Local build and push
```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -t docker.prayujt.com/nimbus:latest \
  -t docker.prayujt.com/nimbus:$(git rev-parse --short HEAD) \
  --push .
```
