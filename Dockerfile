# Build stage
FROM --platform=$BUILDPLATFORM golang:1.25.8 AS build
WORKDIR /build

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags="-s -w" -tags nethttpomithttp2 -o server ./cmd

# Minimal image
FROM alpine:latest
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata
COPY configs/config.yaml configs/config.yaml
COPY --from=build /build/server server
CMD ["./server"]
