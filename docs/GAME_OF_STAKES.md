# ⚔️ Thrylos Game of Stakes: Incentivized Testnet Rules

**Objective:** Break the network to earn Mainnet tokens.
**Duration:** 4 Weeks
**Reward Pool:** 1,000,000 THRY (1% of Supply)

## 🏆 Scoring Categories

| Category | Goal | Reward (Points) |
| :--- | :--- | :--- |
| **Uptime** | Maintain >99% uptime for 4 weeks | 1,000 |
| **Slashing Defense** | Survive a simulated double-sign attack (see tools) | 5,000 |
| **Economic Stress** | Submit >10k txs/hour to spike gas prices | 100 / batch |
| **Bug Hunter** | Find a way to halt the chain (panic/segfault) | 50,000 (Critical) |

## 🚫 Prohibited Behavior
- DDoS attacks against the bootnode IP directly (P2P spam is allowed).
- Social engineering attacks against other validators.

## 🧪 Official Attack Drills
The Foundation will trigger the following events. Validators must survive:
1.  **The Blackout:** Bootnodes will go offline for 10 minutes.
2.  **The Flood:** 1M transactions will be broadcast in 1 hour.
3.  **The Doppelgänger:** We will spin up nodes with *your* public keys (if you leak them) to force slashing.