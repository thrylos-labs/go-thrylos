package api

import (
	"fmt"
	"net/http"
)

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
