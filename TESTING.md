# 🧪 How to Test Thrylos Locally with MetaMask

This guide explains how to connect MetaMask to your local Thrylos node, import the genesis account, and run transactions.

## 📋 Prerequisites

1.  **Thrylos Node Running:**
    Ensure your node is started and the RPC is listening on port `8545`.
    ```bash
    export DYLD_LIBRARY_PATH=$PWD/lib:$DYLD_LIBRARY_PATH
    ./thrylos --data-dir ./data --http --http.port 8545
    ```

2.  **Genesis Private Key:**
    Locate the private key for the pre-funded account defined in your `config.json` or genesis block.

---

## ⚙️ Step 1: Add Network to MetaMask

1.  Open **MetaMask**.
2.  Click the **Network Selector** (top-left) → **Add Network**.
3.  Select **Add a network manually** (bottom of list).
4.  Enter the following configuration:

| Field | Value |
| :--- | :--- |
| **Network Name** | `Thrylos Local` |
| **New RPC URL** | `http://localhost:8545` |
| **Chain ID** | `1` *(Ignore warning about Mainnet)* |
| **Currency Symbol** | `THR` |
| **Decimals** | **9** *(Important: Thrylos uses 9 decimals)* |

5.  Click **Save**.

---

## 🔑 Step 2: Import Genesis Account

1.  In MetaMask, click the **Accounts** menu (top-middle).
2.  Select **Add account or hardware wallet**.
3.  Select **Import account**.
4.  Paste your **Genesis Private Key** (remove `0x` prefix if present).
5.  Click **Import**.

> **Success:** You should see a balance (e.g., `1,000,000 THR`).

---

## 💸 Step 3: Send a Test Transaction

1.  Create a **Account 2** in MetaMask (to receive funds).
2.  Switch back to **Account 1** (Genesis).
3.  Click **Send** and select **Account 2**.
4.  Enter Amount: `10 THR`.
5.  Click **Next** → **Confirm**.
6.  Wait for the transaction to confirm (~1-5 seconds).

---

## 📝 Step 4: Deploy a Smart Contract (Remix)

1.  Go to [Remix IDE](https://remix.ethereum.org/).
2.  Create `Token.sol`:
    ```solidity
    // SPDX-License-Identifier: MIT
    pragma solidity ^0.8.20;
    import "@openzeppelin/contracts/token/ERC20/ERC20.sol";

    contract MyToken is ERC20 {
        constructor() ERC20("MyToken", "MTK") {
            _mint(msg.sender, 1000 * 10 ** decimals());
        }
    }
    ```
3.  **Compile** the contract.
4.  Go to the **Deploy** tab.
5.  Set **Environment** to `Injected Provider - MetaMask`.
6.  Click **Deploy** and confirm in MetaMask.

---

## 🛠️ Troubleshooting

### "Nonce too low" Error
If you restart your local node, MetaMask gets confused because the chain reset but its internal counter didn't.
* **Fix:** Settings → Advanced → **Clear activity tab data**.

### "Internal JSON-RPC Error"
* Check your terminal logs for panic messages.
* Try manually increasing the **Gas Limit** in MetaMask.

### Balance Looks Wrong (Too Small/Big)
* Thrylos uses 9 decimals ($10^9$). Standard Ethereum uses 18.
* **Fix:** Edit the network in MetaMask and ensure **Decimals** is set to `9`.