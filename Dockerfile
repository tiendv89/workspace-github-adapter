# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Download dependencies before copying source so layers are cached on module changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/adapter-service ./cmd/adapter-service && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/adapter-worker ./cmd/adapter-worker && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/migrate ./cmd/migrate

# Runtime stage — distroless for minimal attack surface.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/adapter-service /adapter-service
COPY --from=builder /out/adapter-worker /adapter-worker
COPY --from=builder /out/migrate /migrate

EXPOSE 8080

CMD ["/adapter-service"]
