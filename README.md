# Thrylos Blockchain

**Thrylos** is a high-performance, sharded Proof-of-Stake (PoS) blockchain engine written in Go. It features a modern architecture designed for scalability, featuring dynamic inflation, slashing penalties, and Ethereum-compatible address formats.

## 🌟 Key Features

* **Consensus:** Proof of Stake (PoS) with validator rotation, delegation, and economic finality (Casper FFG-inspired).
* **Networking:** Robust P2P layer built on **libp2p** using GossipSub for message propagation and Kademlia DHT for peer discovery.
* **Storage:** High-performance persistence using **BadgerDB v3**.
* **Security:**
  * **Slashing:** Automated penalties for double-voting, surround-voting, and downtime.
  * **Replay Protection:** Nonce-based and finalized-block-hash protection.
* **Cryptography:** secp256k1 signatures (Ethereum-compatible) with Keccak-256 for transaction signing and address derivation, and Blake2b for consensus hashing.
* **Tokenomics:** Dynamic inflation model targeting a specific bonding ratio, with separate pools for validators, liquidity, and development.
* **Compatibility:** Uses standard **Ethereum 0x addresses** (20 bytes) for wallet compatibility.
* **Sharding:** Native support for sharded state management and cross-shard transfers.

---

## 🛠 Prerequisites

Before building Thrylos, ensure you have the following installed:

* **Go**: Version 1.24.0 or higher.
* **Protocol Buffers**: Required for generating gRPC code.
  * `protoc` compiler.
  * `protoc-gen-go` plugin.
* **OpenSSL**: For generating TLS certificates (needed if you want to run the API in secure (HTTPS) mode in production-style setups).

---

## 🚀 Installation & Build

1. **Clone the repository:**
   ```bash
   git clone https://github.com/thrylos-labs/go-thrylos
   cd go-thrylos
   ```

2. **Install Dependencies:**
   ```bash
   make deps
   ```

3. **Generate Protocol Buffers:**
   ```bash
   make proto
   ```

4. **Build Binaries**

   You now have two distinct build modes:

   **Development build (devnet, deterministic keys, faucet)**

   The dev build uses deterministic validator keys and auto-creates a small 3-validator genesis for local testing. It is never meant for real networks.

   ```bash
   go build -tags dev -o bin/thrylos-dev ./cmd/thrylos
   ```

   **Production build (no dev shortcuts, no faucet, TLS-enforced API)**

   The production build has:
   - No deterministic keys compiled in.
   - No auto-genesis of validators.
   - No faucet endpoint, even if configured.
   - Enforced TLS if the HTTP API is enabled in production-like environments.

   ```bash
   go build -o bin/thrylos ./cmd/thrylos
   ```

5. **🔐 TLS Certificates (for prod-style API)**

   If you plan to run the API server with TLS enabled (HTTPS), you must generate certificates, for example:

   ```bash
   openssl req -x509 -newkey rsa:4096 \
     -keyout server.key \
     -out server.crt \
     -days 365 -nodes \
     -subj "/CN=localhost"
   ```

   You'll then point the API config to `server.crt` and `server.key`.

---

### 🔐 TLS Certificates in Production

Thrylos does **not** ship with any TLS certificates or private keys. You must provide them per environment:

- Certificates and keys are expected at:

  ```text
  ./certs/server.crt
  ./certs/server.key


## 🏃‍♂️ Running the Node

Thrylos now has two entrypoints:
- `bin/thrylos-dev` – development build (with `-tags dev`)
- `bin/thrylos` – production build (default Go build)

### 1. Development Mode (local devnet)

Development mode is designed for local testing:
- Uses deterministic validator keys.
- Auto-creates a 3-validator genesis.
- Can enable the faucet (`/api/v1/fund`).
- Runs HTTP API without TLS.
- Protected by `THRYLOS_ENVIRONMENT=development`.

#### Start a single dev node

```bash
THRYLOS_ENVIRONMENT=development \
bin/thrylos-dev -node 1 -p2p-port 9001
```

This will:
- Use a deterministic key for node 1.
- Create a local data directory: `./data-node1`.
- Start P2P on port 9001.
- Start the HTTP API on port 8080.
- Initialize a small devnet genesis with 3 validators.

You should then be able to query:

```bash
curl http://127.0.0.1:8080/api/v1/status
curl http://127.0.0.1:8080/api/v1/health
```

#### Multi-node devnet (3 validators)

You can run three nodes with deterministic keys:

```bash
# Node 1 (bootstrap)
THRYLOS_ENVIRONMENT=development \
bin/thrylos-dev -node 1 -p2p-port 9001

# Node 2
THRYLOS_ENVIRONMENT=development \
bin/thrylos-dev -node 2 -p2p-port 9002 -bootstrap /ip4/127.0.0.1/tcp/9001/p2p/<node1-peer-id>

# Node 3
THRYLOS_ENVIRONMENT=development \
bin/thrylos-dev -node 3 -p2p-port 9003 -bootstrap /ip4/127.0.0.1/tcp/9001/p2p/<node1-peer-id>
```

**Note:** by default, all nodes try to use API port 8080. For multi-node setups on one machine, you should either:
- Run only one node with the API enabled, or
- Adjust API ports in config per node.

#### Dev-only Faucet

In development builds with `THRYLOS_ENVIRONMENT=development` and `api.enable_faucet = true` in config, the `/api/v1/fund` endpoint is enabled:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/fund \
  -H 'Content-Type: application/json' \
  -d '{"address":"0xYourAddressHere","amount":1000000000}'
```

The faucet is never available in production builds, even if enabled in config.

#### Dev CLI Flags

`bin/thrylos-dev` supports the following dev-oriented flags:

| Flag | Description | Default |
|------|-------------|---------|
| `-node` | Node ID (1, 2, or 3) for deterministic dev keys | 1 |
| `-p2p-port` | P2P TCP port | 9000 |
| `-data` | Data directory | `./data-nodeN` |
| `-bootstrap` | Comma-separated bootstrap peers | "" |
| `-validator` | Run as an active validator | true |

(Plus standard Go logging flags like `-logtostderr`, `-v`, etc.)

### 2. Production Mode (testnet / mainnet-style)

Production builds are meant for any non-local, non-ephemeral network:
- No dev-only deterministic validator keys.
- No auto-generated validator set – you must explicitly provide validator keys and genesis config.
- No faucet (`/fund` route is disabled in prod builds, even if config says otherwise).
- If `THRYLOS_ENVIRONMENT` is a production-like value (`production`, `prod`, `mainnet`), and the HTTP API is enabled, TLS is required.

#### Quick "headless" prod node (no API)

The simplest way to start a prod-style node is with the API disabled in config:

In your config (e.g. `config/mainnet.json`), set:

```json
"api": {
  "enable_api": false,
  ...
}
```

Start the node:

```bash
THRYLOS_ENVIRONMENT=production \
bin/thrylos \
  -validator \
  -validator-key /path/to/validator.key
```

Where `/path/to/validator.key` is a hex-encoded private key understood by Thrylos.

#### Prod node with HTTPS API

To run a production node with an HTTPS API:

1. Configure the API in config:

   ```json
   "api": {
     "enable_api": true,
     "rest_addr": ":8080",
     "enable_tls": true,
     "cert_file": "/path/to/server.crt",
     "key_file": "/path/to/server.key",
     "enable_faucet": false
   }
   ```

2. Start the node:

   ```bash
   THRYLOS_ENVIRONMENT=production \
   bin/thrylos \
     -validator \
     -validator-key /path/to/validator.key \
     -env production
   ```

3. Query status over HTTPS:

   ```bash
   curl -k https://127.0.0.1:8080/api/v1/status
   ```

If you try to run in production/mainnet with:
- `api.enable_api = true`
- `api.enable_tls = false`

the node will refuse to start with a clear log message:

```
API is enabled but TLS is disabled in "production" environment; aborting startup
```

#### Prod CLI Flags

`bin/thrylos` (production binary) supports:

| Flag | Description | Default |
|------|-------------|---------|
| `-data` | Data directory (overrides config) | from config |
| `-p2p-port` | P2P listen port | 9000 |
| `-bootstrap` | Comma-separated bootstrap peers | config P2P peers |
| `-validator` | Run this node as a validator | false |
| `-validator-key` | Path to hex-encoded validator private key file | required if `-validator` is true |
| `-env` | Environment override (mainnet, testnet, devnet, production, etc.) | `$THRYLOS_ENVIRONMENT` or config |
| `-logtostderr` | Log to stderr instead of files | |
| `-alsologtostderr` | Log to stderr as well as files | |
| `-v`, `-vmodule` | Go logging verbosity controls | |

**Important:** Production builds do not contain any of the dev-only deterministic key / debug genesis logic. You must provide real keys and a proper genesis configuration.

---

## 🔌 API Documentation

Thrylos exposes a RESTful JSON API (default `rest_addr` is `:8080` in config).

### Node Status

* `GET /api/v1/status`: Returns current height, peers, and sync status.
* `GET /api/v1/health`: Simple health check.

### Accounts & Balances

* `GET /api/v1/account/{address}`: Get full account details (balance, nonce, stake).
* `GET /api/v1/account/{address}/balance`: Get spendable balance.

### Transactions

* `GET /api/v1/transaction/{hash}`: Get transaction details and status.
* `POST /api/v1/transaction/broadcast`: Submit a signed transaction.

### Faucet (DevNet only)

* `POST /api/v1/fund`: Faucet to fund an address.

This endpoint is only active when:
- You are running the dev build (`thrylos-dev`), and
- `THRYLOS_ENVIRONMENT` is a development-like value (e.g. `development`), and
- `api.enable_faucet = true` in the config.

In production builds, `/fund` is always disabled, regardless of config.

### Staking

* `GET /api/v1/validators`: List all registered validators.
* `GET /api/v1/staking/stats`: Global staking statistics (total staked, APY, etc).

---

## 🏗 Architecture Overview

### Folder Structure

* `cmd/thrylos`: Entrypoints (`main_dev.go` and `main_prod.go` via build tags).
* `consensus/`: PoS logic, fork choice rules, slashing evidence, time validation.
* `core/`: Core blockchain primitives (Blocks, Transactions, State).
* `network/`: P2P networking layer (Libp2p implementation).
* `storage/`: BadgerDB implementation and database abstractions.
* `api/`: HTTP REST API server.
* `proto/`: Protobuf definitions for serialization.

### Tokenomics

* **Total Supply**: 100,000,000 THRYLOS.
* **Base Unit**: 1 THRYLOS = 1,000,000,000 nano.
* **Inflation**: Dynamic (targeting ~4% annually), adjusting based on the staking ratio.
* **Slashing** (configurable):
    * Double Sign: configurable % penalty.
    * Downtime: progressive penalties and jailing based on missed attestations.

---

## 🔒 Security Invariants

Thrylos enforces the following security guarantees across build modes:

* **Production API requires TLS**: If `THRYLOS_ENVIRONMENT` is set to a production-like value (`production`, `prod`, `mainnet`) and the API is enabled, TLS must be configured. The node will refuse to start otherwise.
* **Deterministic keys never in production**: The development build's deterministic validator keys are compile-time excluded from production binaries via build tags. Production nodes must always use explicitly provided validator keys.
* **No faucet in production**: The `/api/v1/fund` faucet endpoint is disabled at compile-time in production builds, regardless of configuration settings.
* **Environment-based protections**: Critical features (faucet, non-TLS API) are gated by `THRYLOS_ENVIRONMENT` checks to prevent accidental misconfiguration.

These invariants ensure that production validators cannot accidentally run with insecure configurations or development-only features.

### 🔑 Validator Keys

Thrylos validators use secp256k1 private keys, stored on disk as **hex-encoded** strings.

- A validator key file is a plain text file containing a single hex-encoded private key, for example:

  ```text
  0xabcdef1234...deadbeef

---

## 🛠 Development Utilities

### Export Codebase to Text

To generate a single text file containing the entire codebase (useful for documentation or LLM context), use the following command:

```bash
codebase-to-text \
  --input "https://github.com/thrylos-labs/go-thrylos" \
  --output "$HOME/Downloads/go-thrylos.txt" \
  --output_type "txt"
```

---

## 🤝 Contributing

1. Fork the repository.
2. Create your feature branch (`git checkout -b feature/amazing-feature`).
3. Commit your changes (`git commit -m 'Add amazing feature'`).
4. Run tests (`make test` or `go test ./...`).
5. Push to the branch.
6. Open a Pull Request.

---

## 📄 License

Thrylos is open-source software.