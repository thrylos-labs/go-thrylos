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
	coremath "github.com/thrylos-labs/go-thrylos/core/math"
	"github.com/thrylos-labs/go-thrylos/core/state"
	"github.com/thrylos-labs/go-thrylos/core/transaction"
	thryloscrypto "github.com/thrylos-labs/go-thrylos/crypto"
	core "github.com/thrylos-labs/go-thrylos/proto/core"
	"github.com/thrylos-labs/go-thrylos/storage"
)

func TestGovernanceProposalEndpoint_AcceptsPreSignedTransaction(t *testing.T) {
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
		Balance:      coremath.ParseBigInt("1000000").Bytes(),
		Nonce:        0,
		StakedAmount: nil,
		DelegatedTo:  map[string][]byte{},
		Rewards:      nil,
	})
	require.NoError(t, err)

	server := NewServerWithConfig(ws, nil, nil, cfg, nil)
	txValidator := transaction.NewValidator(ws.GetShardID(), ws.GetTotalShards(), cfg)

	tx, err := txValidator.CreateGovernanceProposalTransaction(
		from,
		"economics.community_tax",
		"0.04",
		21000,
		cfg.Economics.BaseGasPrice,
		0,
		privateKey,
	)
	require.NoError(t, err)

	body, err := json.Marshal(map[string]interface{}{
		"tx":        tx,
		"broadcast": false,
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
	require.Equal(t, "validated", response.Status)
	require.False(t, response.Broadcast)
	require.NotEmpty(t, response.TxHash)
	require.NotNil(t, response.Tx)
	require.Equal(t, core.TransactionType_GOVERNANCE_PROPOSE, response.Tx.Type)

	var payload map[string]string
	require.NoError(t, json.Unmarshal(response.Tx.Data, &payload))
	require.Equal(t, "economics.community_tax", payload["parameter"])
	require.Equal(t, "0.04", payload["proposed_value"])
}

func TestGovernanceProposalEndpoint_RejectsPrivateKeyField(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Environment = "development"

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

	ws, err := state.NewWorldState(tmpDir, accountpkg.ShardID(0), 1, cfg, badgerStore)
	require.NoError(t, err)

	server := NewServerWithConfig(ws, nil, nil, cfg, nil)

	body := []byte(`{"private_key":"secret","broadcast":false}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/governance/propose", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "private_key is not accepted")
}
