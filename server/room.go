package main

import "sync"

// Room holds all peers in a room
type Room struct {
	ID    string
	Peers map[string]*Peer
	mu    sync.RWMutex
}

// AddPeer adds a peer to the room
func (r *Room) AddPeer(peer *Peer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Peers[peer.ID] = peer
	peer.Room = r
}

// RemovePeer removes a peer from the room
func (r *Room) RemovePeer(peerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.Peers, peerID)
}

// GetOtherPeers returns all peers except the one with excludeID
func (r *Room) GetOtherPeers(excludeID string) []*Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()

	peers := make([]*Peer, 0)
	for id, peer := range r.Peers {
		if id != excludeID {
			peers = append(peers, peer)
		}
	}
	return peers
}

// BroadcastExcept sends a message to all peers except the one with excludeID
func (r *Room) BroadcastExcept(excludeID string, msg SignalMessage) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for id, peer := range r.Peers {
		if id != excludeID {
			peer.SendMessage(msg)
		}
	}
}

// GetPeer returns the peer with the given ID
func (r *Room) GetPeer(peerID string) *Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.Peers[peerID]
}

// RoomManager manages all rooms
type RoomManager struct {
	Rooms map[string]*Room
	mu    sync.RWMutex
}

// GetOrCreateRoom returns an existing room or creates a new one
func (rm *RoomManager) GetOrCreateRoom(roomID string) *Room {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if room, exists := rm.Rooms[roomID]; exists {
		return room
	}

	room := &Room{
		ID:    roomID,
		Peers: make(map[string]*Peer),
	}
	rm.Rooms[roomID] = room
	return room
}

// Global room manager instance
var roomManager = &RoomManager{
	Rooms: make(map[string]*Room),
}
