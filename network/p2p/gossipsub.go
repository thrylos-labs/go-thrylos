// network/p2p/gossipsub.go
// FIND-06: Hardened GossipSub with peer scoring and rate limiting.

package p2p

import (
	"context"
	"fmt"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	gossipMaxMessageBytes  = 1 * 1024 * 1024 // 1 MB
	scoreGossipThreshold   = -10.0
	scorePublishThreshold  = -50.0
	scoreGraylistThreshold = -100.0

	meshDeliveriesDecay  = 0.97
	meshDeliveriesWeight = -1.0
)

// NewHardenedGossipSub replaces the bare pubsub.NewGossipSub call.
// Pass the already-initialised MessageValidator so rate limiting is
// applied at the RPC boundary before messages enter the router.
func NewHardenedGossipSub(ctx context.Context, h host.Host, validator *MessageValidator) (*pubsub.PubSub, error) {

	peerScoreParams := &pubsub.PeerScoreParams{
		AppSpecificScore:            func(p peer.ID) float64 { return 0 },
		DecayInterval:               time.Minute,
		DecayToZero:                 0.01,
		RetainScore:                 6 * time.Hour,
		IPColocationFactorThreshold: 3,
		IPColocationFactorWeight:    -10,
		BehaviourPenaltyWeight:      -10,
		BehaviourPenaltyDecay:       0.986,
		Topics: map[string]*pubsub.TopicScoreParams{
			TopicBlocks:       blockTopicScoreParams(),
			TopicTransactions: txTopicScoreParams(),
			TopicAttestations: lightTopicScoreParams(),
			TopicVotes:        lightTopicScoreParams(),
			TopicValidators:   lightTopicScoreParams(),
		},
	}

	scoreThresholds := &pubsub.PeerScoreThresholds{
		GossipThreshold:             scoreGossipThreshold,
		PublishThreshold:            scorePublishThreshold,
		GraylistThreshold:           scoreGraylistThreshold,
		AcceptPXThreshold:           0,
		OpportunisticGraftThreshold: 1.0,
	}

	opts := []pubsub.Option{
		pubsub.WithPeerScore(peerScoreParams, scoreThresholds),
		pubsub.WithMaxMessageSize(gossipMaxMessageBytes),
		pubsub.WithFloodPublish(false),
	}

	if validator != nil {
		opts = append(opts, pubsub.WithAppSpecificRpcInspector(
			func(peerID peer.ID, rpc *pubsub.RPC) error {
				if err := validator.CheckPeerStatus(peerID); err != nil {
					return fmt.Errorf("rate limit: %w", err)
				}
				return nil
			},
		))
	}

	return pubsub.NewGossipSub(ctx, h, opts...)
}

func blockTopicScoreParams() *pubsub.TopicScoreParams {
	return &pubsub.TopicScoreParams{
		TopicWeight:                     1.0,
		TimeInMeshWeight:                0.01,
		TimeInMeshQuantum:               time.Second,
		TimeInMeshCap:                   3600,
		FirstMessageDeliveriesWeight:    1,
		FirstMessageDeliveriesDecay:     0.9,
		FirstMessageDeliveriesCap:       100,
		MeshMessageDeliveriesWeight:     meshDeliveriesWeight,
		MeshMessageDeliveriesDecay:      meshDeliveriesDecay,
		MeshMessageDeliveriesThreshold:  5,
		MeshMessageDeliveriesCap:        50,
		MeshMessageDeliveriesActivation: 30 * time.Second,
		MeshMessageDeliveriesWindow:     10 * time.Millisecond,
		MeshFailurePenaltyWeight:        -1,
		MeshFailurePenaltyDecay:         0.97,
		InvalidMessageDeliveriesWeight:  -100,
		InvalidMessageDeliveriesDecay:   0.99,
	}
}

func txTopicScoreParams() *pubsub.TopicScoreParams {
	return &pubsub.TopicScoreParams{
		TopicWeight:                     0.5,
		TimeInMeshWeight:                0.01,
		TimeInMeshQuantum:               time.Second,
		TimeInMeshCap:                   3600,
		FirstMessageDeliveriesWeight:    1,
		FirstMessageDeliveriesDecay:     0.9,
		FirstMessageDeliveriesCap:       2000,
		MeshMessageDeliveriesWeight:     meshDeliveriesWeight,
		MeshMessageDeliveriesDecay:      meshDeliveriesDecay,
		MeshMessageDeliveriesThreshold:  20,
		MeshMessageDeliveriesCap:        100,
		MeshMessageDeliveriesActivation: 30 * time.Second,
		MeshMessageDeliveriesWindow:     10 * time.Millisecond,
		MeshFailurePenaltyWeight:        -1,
		MeshFailurePenaltyDecay:         0.97,
		InvalidMessageDeliveriesWeight:  -100,
		InvalidMessageDeliveriesDecay:   0.99,
	}
}

func lightTopicScoreParams() *pubsub.TopicScoreParams {
	return &pubsub.TopicScoreParams{
		TopicWeight:                    0.2,
		TimeInMeshWeight:               0.01,
		TimeInMeshQuantum:              time.Second,
		TimeInMeshCap:                  3600,
		FirstMessageDeliveriesWeight:   0.5,
		FirstMessageDeliveriesDecay:    0.9,
		FirstMessageDeliveriesCap:      500,
		InvalidMessageDeliveriesWeight: -50,
		InvalidMessageDeliveriesDecay:  0.99,
	}
}
