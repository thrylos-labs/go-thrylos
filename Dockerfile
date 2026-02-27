# Build Stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install build dependencies including Rust
RUN apk add --no-cache make git gcc musl-dev linux-headers curl llvm

# ── FIND-09: Pinned Rust installation with checksum verification ──────────────
#
# BEFORE (insecure):
#   RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
#
# Piping curl directly into sh is a supply chain risk — if sh.rustup.rs or
# any CDN serving it were compromised, attacker code runs with full build
# privileges and could embed malicious code in the compiled binary.
#
# AFTER: Download rustup-init and its official SHA-256 separately, verify
# the digest before executing, then confirm the installed version matches.
#
# Architecture-aware: detects x86_64 vs aarch64 at build time so the same
# Dockerfile works on both Intel/AMD and Apple Silicon/ARM servers.
# ─────────────────────────────────────────────────────────────────────────────
ARG RUST_VERSION=1.85.0

RUN set -eux; \
    ARCH="$(uname -m)"; \
    case "${ARCH}" in \
        x86_64)  TRIPLE="x86_64-unknown-linux-musl" ;; \
        aarch64) TRIPLE="aarch64-unknown-linux-musl" ;; \
        *) echo "Unsupported arch: ${ARCH}" && exit 1 ;; \
    esac; \
    RUSTUP_URL="https://static.rust-lang.org/rustup/dist/${TRIPLE}/rustup-init"; \
    curl -Lo /tmp/rustup-init        "${RUSTUP_URL}"; \
    curl -Lo /tmp/rustup-init.sha256 "${RUSTUP_URL}.sha256"; \
    cd /tmp && sha256sum -c rustup-init.sha256; \
    chmod +x /tmp/rustup-init; \
    /tmp/rustup-init -y \
      --default-toolchain "${RUST_VERSION}" \
      --profile minimal \
      --no-modify-path; \
    rm /tmp/rustup-init /tmp/rustup-init.sha256

ENV PATH="/root/.cargo/bin:${PATH}"

# Confirm the installed toolchain matches the pinned version
RUN rustc --version | grep -F "${RUST_VERSION}"

# ── Go dependencies (cached layer) ───────────────────────────────────────────
COPY go.mod go.sum ./
RUN go mod download

# ── Source code ──────────────────────────────────────────────────────────────
COPY . .

# ── Build REVM library ───────────────────────────────────────────────────────
WORKDIR /app/revm_wrapper
RUN AR=ar cargo build --release --verbose 2>&1 | tail -20 && \
    mkdir -p /app/lib && \
    if [ -f target/release/libthrylos_revm.a ]; then \
        # Check if thin archive and convert to fat if needed
        if ar t target/release/libthrylos_revm.a 2>&1 | grep -q "thin"; then \
            ar -rcT /app/lib/libthrylos_revm.a target/release/libthrylos_revm.a; \
        else \
            cp target/release/libthrylos_revm.a /app/lib/libthrylos_revm.a; \
        fi; \
        ar s /app/lib/libthrylos_revm.a; \
    else \
        echo "ERROR: No REVM library found!" && exit 1; \
    fi

# ── Build Go binary ──────────────────────────────────────────────────────────
WORKDIR /app
ENV CGO_ENABLED=1
RUN mkdir -p /app/bin && \
    go build -tags dev -v -o /app/bin/thrylos ./cmd/thrylos

# ── Runner Stage ─────────────────────────────────────────────────────────────
FROM alpine:latest

WORKDIR /app

# FIND-09: curl removed from runner stage — not needed at runtime and
# reduces the attack surface of the production image.
RUN apk add --no-cache bash libgcc libstdc++

COPY --from=builder /app/bin/thrylos /usr/local/bin/thrylos
COPY --from=builder /app/lib/libthrylos_revm.a /usr/local/lib/libthrylos_revm.a
RUN ldconfig /usr/local/lib 2>/dev/null || true

COPY entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

RUN mkdir -p /app/data /app/config /app/keys /app/certs

EXPOSE 9000 8080

ENTRYPOINT ["/app/entrypoint.sh"]