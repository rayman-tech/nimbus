FROM --platform=$BUILDPLATFORM golang:1.25.1-alpine AS build
ARG TARGETOS
ARG TARGETARCH

WORKDIR /app

COPY . .

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /app/bin/nimbus ./cmd


FROM alpine:3.22
RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=build /app/bin/nimbus /app/nimbus

EXPOSE 8080

ENTRYPOINT ["/app/nimbus"]
