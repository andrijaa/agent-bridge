package main

import (
	"log"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

// Peer represents a connected client
type Peer struct {
	ID             string
	Conn           *websocket.Conn
	PeerConnection *webrtc.PeerConnection
	Room           *Room
	LocalTracks    map[string]*webrtc.TrackLocalStaticRTP
	mu             sync.Mutex
}

// SendMessage sends a signaling message to the peer
func (p *Peer) SendMessage(msg SignalMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Conn.WriteJSON(msg)
}

// addTrackToPeer adds a track to the peer and triggers renegotiation
func addTrackToPeer(peer *Peer, track *webrtc.TrackLocalStaticRTP, _ string) {
	sender, err := peer.PeerConnection.AddTrack(track)
	if err != nil {
		log.Printf("Failed to add track to peer %s: %v", peer.ID, err)
		return
	}

	// Read and discard RTCP packets to keep the connection alive
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buf); err != nil {
				return
			}
		}
	}()

	// Trigger renegotiation
	triggerNegotiation(peer)
}
