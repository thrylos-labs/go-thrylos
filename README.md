# Thrylos Blockchain

**Thrylos** is a high-performance, sharded Proof-of-Stake (PoS) blockchain engine written in Go. It features a modern architecture designed for scalability, featuring dynamic inflation, slashing penalties, and Ethereum-compatible address formats.

## 🌟 Key Features

* **Consensus:** Proof of Stake (PoS) with validator rotation, delegation, and economic finality (Casper FFG-inspired).
* **Networking:** Robust P2P layer built on **libp2p** using GossipSub for message propagation and Kademlia DHT for peer discovery.
* **Storage:** High-performance persistence using **BadgerDB v3**.
* **Security:**
  * **Slashing:** Slashing: Automated penalties for double-voting and downtime. Note: Surround-voting detection is currently disabled for Testnet and scheduled for a future protocol upgrade.  
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

### 1. Clone the repository

```bash
git clone https://github.com/thrylos-labs/go-thrylos
cd go-thrylos
```

### 2. Install Dependencies

```bash
make deps
```

### 3. Generate Protocol Buffers

```bash
make proto
```

### 4. Build Binaries

You now have two distinct build modes:

#### Development build (devnet, deterministic keys, faucet)

The dev build uses deterministic validator keys and auto-creates a small 3-validator genesis for local testing. It is never meant for real networks.

```bash
go build -tags dev -o bin/thrylos-dev ./cmd/thrylos
```

#### Production build (no dev shortcuts, no faucet, TLS-enforced API)

The production build has:
- No deterministic keys compiled in.
- No auto-genesis of validators.
- No faucet endpoint, even if configured.
- Enforced TLS if the HTTP API is enabled in production-like environments.

```bash
go build -o bin/thrylos ./cmd/thrylos
```

---

#### 🐳 Docker Deployment (Local Testnet)

You can simulate a full 4-node network locally using Docker Compose. This setup automatically generates secure keys, creates a shared genesis file, and peers the nodes together using Node 1 as the bootnode.

Prerequisites: Docker and Docker Compose installed.

1. Start the Cluster Build the images and start the network in detached mode:

Bash

docker-compose up --build -d
2. Verify Connectivity Node 1 exposes the JSON-RPC API on port 8080. Check the status to see block height increasing:

curl http://localhost:8080/api/v1/status


3. Monitor Logs To watch the consensus engine and P2P traffic in real-time:

# Tail logs for the bootnode
docker logs -f go-thrylos-node-1-1

# Tail logs for a specific peer
docker logs -f go-thrylos-node-2-1

# Run in the backgrounds
docker-compose up -d

4. Stop and Clean Stop the nodes (persisting data in Docker volumes):

docker-compose down
Stop and wipe all data (keys, genesis, blockchain history) to start fresh:

docker-compose down -v



## 🛡️ Secure Genesis Setup (Production)

For production or public testnets, never use hardcoded addresses. Follow this process to generate a secure genesis configuration.

### 1. Generate Keys & Genesis

Use the included genesis generator tool to create secure, random validator keys and the initial genesis.json. Perform this on an offline machine (air-gapped).

#### Build the generator

```bash
go build -o gen-genesis ./cmd/genesis-generator
```

#### Run the generator

```bash
# Generates 4 validators and outputs to default paths (./keys and ./config/genesis.json)
./gen-genesis

# Or specify custom options
./gen-genesis -n 5 -o ./production-keys -g ./config/mainnet-genesis.json
```

#### Distribute & Secure

- **Foundation Key**: Move `foundation_cold.key` to cold storage immediately. Do not leave it on a connected server.
- **Validator Keys**: Securely distribute each `validator_N.key` to its respective bootnode server (e.g., via SCP).
- **Genesis File**: Copy the generated `genesis.json` to the `config/` directory on all nodes.

### 2. TLS Certificates (for prod-style API)

If you plan to run the API server with TLS enabled (HTTPS), you must generate certificates. For example:

```bash
openssl req -x509 -newkey rsa:4096 \
  -keyout server.key \
  -out server.crt \
  -days 365 -nodes \
  -subj "/CN=localhost"
```

You must then point the API config (in `config.go` or `config/mainnet.json`) to `server.crt` and `server.key`. Thrylos expects these certificates to be present if `api.enable_tls` is true.

---

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

#### Dev-only Faucet

In development builds with `THRYLOS_ENVIRONMENT=development` and `api.enable_faucet = true` in config, the `/api/v1/fund` endpoint is enabled:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/fund \
  -H 'Content-Type: application/json' \
  -d '{"address":"0xYourAddressHere","amount":1000000000}'
```

**Note:** The faucet is never available in production builds, even if enabled in config.

#### Dev CLI Flags

`bin/thrylos-dev` supports the following dev-oriented flags:

| Flag | Description | Default |
|------|-------------|---------|
| `-node` | Node ID (1, 2, or 3) for deterministic dev keys | 1 |
| `-p2p-port` | P2P TCP port | 9000 |
| `-data` | Data directory | `./data-nodeN` |
| `-bootstrap` | Comma-separated bootstrap peers | "" |
| `-validator` | Run as an active validator | true |

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

Start the node using the key generated in the "Secure Genesis Setup" step:

```bash
THRYLOS_ENVIRONMENT=production \
bin/thrylos \
  -validator \
  -validator-key ./keys/validator_1.key
```

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
  -validator-key ./keys/validator_1.key \
  -env production
```

3. Query status over HTTPS:

```bash
curl -k https://127.0.0.1:8080/api/v1/status
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

- `GET /api/v1/status`: Returns current height, peers, and sync status.
- `GET /api/v1/health`: Simple health check.

### Accounts & Balances

- `GET /api/v1/account/{address}`: Get full account details (balance, nonce, stake).
- `GET /api/v1/account/{address}/balance`: Get spendable balance.

### Transactions

- `GET /api/v1/transaction/{hash}`: Get transaction details and status.
- `POST /api/v1/transaction/broadcast`: Submit a signed transaction.

### Staking

- `GET /api/v1/validators`: List all registered validators.
- `GET /api/v1/staking/stats`: Global staking statistics (total staked, APY, etc).

---

## 🏗 Architecture Overview

### Folder Structure

- `cmd/thrylos`: Entrypoints (`main_dev.go` and `main_prod.go` via build tags).
- `cmd/genesis-generator`: Tool for creating secure genesis configurations.
- `consensus/`: PoS logic, fork choice rules, slashing evidence, time validation.
- `core/`: Core blockchain primitives (Blocks, Transactions, State).
- `network/`: P2P networking layer (Libp2p implementation).
- `storage/`: BadgerDB implementation and database abstractions.
- `api/`: HTTP REST API server.
- `proto/`: Protobuf definitions for serialization.

### Tokenomics

- **Total Supply**: 100,000,000 THRYLOS.
- **Base Unit**: 1 THRYLOS = 1,000,000,000 nano.
- **Inflation**: Dynamic (targeting ~4% annually), adjusting based on the staking ratio.
- **Slashing** (configurable):
  - Double Sign: configurable % penalty.
  - Downtime: progressive penalties and jailing based on missed attestations.

---

## 🔒 Security Invariants

Thrylos enforces the following security guarantees across build modes:

- **Production API requires TLS**: If `THRYLOS_ENVIRONMENT` is set to a production-like value (`production`, `prod`, `mainnet`) and the API is enabled, TLS must be configured. The node will refuse to start otherwise.
- **Deterministic keys never in production**: The development build's deterministic validator keys are compile-time excluded from production binaries via build tags. Production nodes must always use explicitly provided validator keys.
- **No faucet in production**: The `/api/v1/fund` faucet endpoint is disabled at compile-time in production builds, regardless of configuration settings.
- **Environment-based protections**: Critical features (faucet, non-TLS API) are gated by `THRYLOS_ENVIRONMENT` checks to prevent accidental misconfiguration.

These invariants ensure that production validators cannot accidentally run with insecure configurations or development-only features.

---

## 🔑 Validator Keys

Thrylos validators use secp256k1 private keys, stored on disk as hex-encoded strings.

A validator key file is a plain text file containing a single hex-encoded private key, for example:

```
0xabcdef1234...deadbeef
```

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