# Thrylos Blockchain

**Thrylos** is a high-performance, sharded Proof-of-Stake (PoS) blockchain engine written in Go. It features a modern architecture designed for scalability, featuring dynamic inflation, slashing penalties, and Ethereum-compatible address formats.

## 🌟 Key Features

* **Consensus:** Proof of Stake (PoS) with validator rotation, delegation, and economic finality (Casper FFG-inspired).
* **Networking:** Robust P2P layer built on **libp2p** using GossipSub for message propagation and Kademlia DHT for peer discovery.
* **Storage:** High-performance persistence using **BadgerDB v3**.
* **Security:**
    * **Slashing:** Automated penalties for double-voting, surround-voting, and downtime.
    * **Replay Protection:** Nonce-based and finalized-block-hash protection.
    * **Cryptography:** Ed25519 signatures and Blake2b hashing.
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
* **OpenSSL**: For generating TLS certificates (optional, for secure API).

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

3. **🔐 Generate Development Certificates:**
   
   If you plan to run the API server with TLS enabled (HTTPS), you must generate self-signed certificates. This is required for the node to start in secure mode.
   
   ```bash
   openssl req -x509 -newkey rsa:4096 -keyout server.key -out server.crt -days 365 -nodes -subj "/CN=localhost"
   ```

4. **Generate Protocol Buffers:**
   ```bash
   make proto
   ```

5. **Build the Binary:**
   ```bash
   make build
   ```
   
   The binary will be output to `./bin/thrylos`.

---

## 🏃‍♂️ Running the Node

### Quick Start (Multi-Node Simulation)

For development, you can spin up a local 3-node cluster using the included debug script:

```bash
./debug-multi-nodes.sh
```

This will start:
* **Node 1 (Bootstrap)**: Port 9001
* **Node 2**: Port 9002 (Peered with Node 1)
* **Node 3**: Port 9003 (Peered with Node 1)

### Manual Start

To run a single node manually:

```bash
./bin/thrylos --node=1 --port=9000 --validator=true
```

### Command Line Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--node` | Node ID (integer, usually 1, 2, or 3 for dev). | 1 |
| `--port` | P2P listening port. | 9000 |
| `--data` | Data directory path. | `../data-nodeN` |
| `--bootstrap` | Comma-separated list of bootstrap peer addresses. | "" |
| `--validator` | Run as an active validator. | true |
| `--api-port` | Port for the HTTP JSON-RPC API. | 8080 |

---

## 🔌 API Documentation

Thrylos exposes a RESTful JSON API (default port 8080).

### Node Status

* `GET /api/v1/status`: Returns current height, peers, and sync status.
* `GET /api/v1/health`: Simple health check.

### Accounts & Balances

* `GET /api/v1/account/{address}`: Get full account details (balance, nonce, stake).
* `GET /api/v1/account/{address}/balance`: Get spendable balance.

### Transactions

* `GET /api/v1/transaction/{hash}`: Get transaction details and status.
* `POST /api/v1/transaction/broadcast`: Submit a signed transaction.
* `POST /api/v1/fund`: (DevNet only) Faucet to fund an address.

### Staking

* `GET /api/v1/validators`: List all registered validators.
* `GET /api/v1/staking/stats`: Global staking statistics (Total staked, APY).

---

## 🏗 Architecture Overview

### Folder Structure

* `cmd/thrylos`: Entry point (main.go).
* `consensus/`: PoS logic, fork choice rules, and slashing evidence.
* `core/`: Core blockchain primitives (Blocks, Transactions, State).
* `network/`: P2P networking layer (Libp2p implementation).
* `storage/`: BadgerDB implementation and database abstractions.
* `api/`: HTTP REST API server.
* `proto/`: Protobuf definitions for serialization.

### Tokenomics

* **Total Supply**: 100,000,000 THRYLOS.
* **Base Unit**: 1 THRYLOS = 1,000,000,000 nano.
* **Inflation**: Dynamic (targeting ~4% annually), adjusting based on the staking ratio.
* **Slashing**:
    * Double Sign: 5% penalty.
    * Downtime: 1% penalty.

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