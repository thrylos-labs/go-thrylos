# Build Stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache make git gcc musl-dev linux-headers

# Copy dependency files first (better caching)
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the main binary and the generator
RUN go build -o /app/bin/thrylos ./cmd/thrylos
RUN go build -o /app/bin/gen-genesis ./cmd/genesis-generator

# Runner Stage
FROM alpine:latest

WORKDIR /app

# Install basic utils (curl for healthchecks)
RUN apk add --no-cache curl bash

# Copy binaries from builder
COPY --from=builder /app/bin/thrylos /usr/local/bin/thrylos
COPY --from=builder /app/bin/gen-genesis /usr/local/bin/gen-genesis

# Copy entrypoint script
COPY entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

# Create data directories
RUN mkdir -p /app/data /app/config /app/keys

# Expose ports (P2P, API, Metrics)
EXPOSE 9000 8080 6060

ENTRYPOINT ["/app/entrypoint.sh"]