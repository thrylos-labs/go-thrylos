# Build Stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install build dependencies including Rust
RUN apk add --no-cache make git gcc musl-dev linux-headers curl

# Install Rust for building REVM
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
ENV PATH="/root/.cargo/bin:${PATH}"

# Copy dependency files first (better caching)
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the REVM library from source
WORKDIR /app/revm_wrapper

# Debug: Show what we're building on
RUN echo "=== Build environment ===" && \
    rustc --version && \
    rustc --print target-list | grep $(rustc -vV | sed -n 's|host: ||p')

# Build the library
RUN cargo build --release --verbose 2>&1 | tail -20

# Debug: Show what was actually built
RUN echo "=== Files in target/release/ ===" && \
    find target/release -name "libthrylos*" -type f -exec ls -lh {} \;

# Create lib directory at the location Go expects
RUN mkdir -p /app/lib

# Copy whatever library we have
RUN if [ -f target/release/libthrylos_revm.so ]; then \
        echo "Found .so file (shared library)"; \
        cp target/release/libthrylos_revm.so /app/lib/libthrylos_revm.so; \
    elif [ -f target/release/libthrylos_revm.a ]; then \
        echo "Found .a file (static library), using it"; \
        cp target/release/libthrylos_revm.a /app/lib/libthrylos_revm.a; \
    else \
        echo "ERROR: No REVM library found!" && exit 1; \
    fi

# Verify what we have

# Build the Go binaries
WORKDIR /app

# If using static library, update CGO flags
ENV CGO_ENABLED=1
RUN go build -tags dev -v -o /app/bin/thrylos ./cmd/thrylos 2>&1 | grep -E "(LDFLAGS|lthrylos)" || true
RUN go build -o /app/bin/gen-genesis ./cmd/genesis-generator

# Runner Stage
FROM alpine:latest

WORKDIR /app

# Install runtime dependencies
RUN apk add --no-cache curl bash libgcc libstdc++

# Copy binaries from builder
COPY --from=builder /app/bin/thrylos /usr/local/bin/thrylos
COPY --from=builder /app/bin/gen-genesis /usr/local/bin/gen-genesis

# Copy the REVM library if it exists (might be statically linked)
COPY --from=builder /app/lib/libthrylos_revm.a /usr/local/lib/libthrylos_revm.a
RUN ldconfig /usr/local/lib 2>/dev/null || true

# Set runtime library path
ENV LD_LIBRARY_PATH="/usr/local/lib:${LD_LIBRARY_PATH}"

# Copy entrypoint script
COPY entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

# Create data directories
RUN mkdir -p /app/data /app/config /app/keys

# Expose ports (P2P, API)
EXPOSE 9000 8080

ENTRYPOINT ["/app/entrypoint.sh"]
