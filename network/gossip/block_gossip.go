// A gossip protocol is a communication protocol used in distributed systems to achieve efficient and reliable data dissemination among nodes.
// Not sure if needed if using libp2p
package gossip

// libp2p is the networking layer — it handles:

// Peer discovery

// Secure transport (TCP, QUIC, WebSockets, etc.)

// Connection multiplexing

// Stream and protocol negotiation

// NAT traversal and relay if needed

// gossipsub is a pub/sub protocol built on top of libp2p — it handles:

// Message broadcasting (like blocks, transactions, consensus votes)

// Peer mesh formation for each topic

// Efficient, resilient propagation (avoids flooding)
