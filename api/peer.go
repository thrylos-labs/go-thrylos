// api/peer.go
//
// GET /api/v1/peer-id
//
// Returns this node's libp2p peer ID as a plain-text string.
// Used by the peer-id-resolver sidecar in docker-compose-testnet.yml so that
// bootstrap multiaddrs can be resolved dynamically at container startup
// instead of being hardcoded in the compose file (FIND-04 remediation).

package api

import (
	"fmt"
	"net/http"
)

// getPeerID handles GET /api/v1/peer-id.
//
// It delegates to peerIDFunc, which is injected at Server construction time
// via NewServerWithConfig. This keeps the api package decoupled from the
// node and network packages — the Server never imports node or p2p directly.
//
// Typical injection in node/node.go:
//
//	api.NewServerWithConfig(ws, blockchain, evmExecutor, cfg, n.GetPeerID)
//
// Response codes:
//   - 200 OK          — body is the peer ID string, e.g. "12D3KooW..."
//   - 503 Unavailable — P2P layer not yet initialised (node still starting)
func (s *Server) getPeerID(w http.ResponseWriter, r *http.Request) {
	if s.peerIDFunc == nil {
		http.Error(w, "P2P not initialised", http.StatusServiceUnavailable)
		return
	}
	id := s.peerIDFunc()
	if id == "" {
		http.Error(w, "P2P not initialised", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, id)
}
