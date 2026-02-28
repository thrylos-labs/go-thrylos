package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thrylos-labs/go-thrylos/config"
	accountpkg "github.com/thrylos-labs/go-thrylos/core/account"
	"github.com/thrylos-labs/go-thrylos/core/state"
	thryloscrypto "github.com/thrylos-labs/go-thrylos/crypto"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
)

func TestGovernanceProposalEndpoint_ConstructsSignedTransaction(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "thrylos-api-governance-*")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.RemoveAll(tmpDir)
	})

	badgerStore, err := storage.NewBadgerStorage(tmpDir)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = badgerStore.Close()
	})

	cfg := config.DefaultConfig()
	cfg.Environment = "development"

	ws, err := state.NewWorldState(tmpDir, accountpkg.ShardID(0), 1, cfg, badgerStore)
	require.NoError(t, err)

	privateKey, err := thryloscrypto.NewPrivateKey()
	require.NoError(t, err)
	from := privateKey.Address().String()

	err = ws.GetAccountManager().UpdateAccount(&core.Account{
		Address:      from,
		Balance:      "1000000",
		Nonce:        0,
		StakedAmount: "0",
		DelegatedTo:  map[string]string{},
		Rewards:      "0",
	})
	require.NoError(t, err)

	server := NewServerWithConfig(ws, nil, nil, cfg, nil)

	body, err := json.Marshal(map[string]interface{}{
		"from":           from,
		"private_key":    privateKey.String(),
		"parameter":      "economics.community_tax",
		"proposed_value": "0.04",
		"broadcast":      false,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/governance/propose", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)

	var response struct {
		Status    string            `json:"status"`
		Broadcast bool              `json:"broadcast"`
		TxHash    string            `json:"tx_hash"`
		Tx        *core.Transaction `json:"tx"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "created", response.Status)
	require.False(t, response.Broadcast)
	require.NotEmpty(t, response.TxHash)
	require.NotNil(t, response.Tx)
	require.Equal(t, core.TransactionType_GOVERNANCE_PROPOSE, response.Tx.Type)

	var payload map[string]string
	require.NoError(t, json.Unmarshal(response.Tx.Data, &payload))
	require.Equal(t, "economics.community_tax", payload["parameter"])
	require.Equal(t, "0.04", payload["proposed_value"])
}
