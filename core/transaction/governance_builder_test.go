package transaction

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	thryloscrypto "github.com/thrylos-labs/go-thrylos/crypto"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
)

func TestCreateGovernanceProposalTransaction_BuildsCanonicalPayload(t *testing.T) {
	cfg := config.DefaultConfig()
	validator := NewValidator(0, 1, cfg)

	privateKey, err := thryloscrypto.NewPrivateKey()
	require.NoError(t, err)

	from := privateKey.Address().String()
	tx, err := validator.CreateGovernanceProposalTransaction(
		from,
		"economics.community_tax",
		"0.04",
		21000,
		cfg.Economics.BaseGasPrice,
		7,
		privateKey,
	)
	require.NoError(t, err)
	require.Equal(t, core.TransactionType_GOVERNANCE_PROPOSE, tx.Type)
	require.Equal(t, "0", tx.Amount)
	require.Equal(t, uint64(7), tx.Nonce)

	var payload map[string]string
	require.NoError(t, json.Unmarshal(tx.Data, &payload))
	require.Equal(t, "economics.community_tax", payload["parameter"])
	require.Equal(t, "0.04", payload["proposed_value"])
	require.NotEmpty(t, tx.Hash)
	require.NotEmpty(t, tx.Signature)
}
