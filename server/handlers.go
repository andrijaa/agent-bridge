package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handleWebSocket handles incoming WebSocket connections
func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	var peer *Peer

	for {
		var msg SignalMessage
		if err := conn.ReadJSON(&msg); err != nil {
			log.Printf("WebSocket read error: %v", err)
			if peer != nil {
				handlePeerDisconnect(peer)
			}
			return
		}

		log.Printf("Received message type: %s from %s", msg.Type, msg.ClientID)

		switch msg.Type {
		case "join":
			peer = handleJoin(conn, msg)
			if peer == nil {
				return
			}

		case "offer":
			if peer != nil {
				handleOffer(peer, msg)
			}

		case "answer":
			if peer != nil {
				handleAnswer(peer, msg)
			}

		case "candidate":
			if peer != nil {
				handleCandidate(peer, msg)
			}

		case "screenshot":
			if peer != nil {
				handleScreenshot(peer, msg)
			}
		}
	}
}

// handleJoin handles a peer joining a room
func handleJoin(conn *websocket.Conn, msg SignalMessage) *Peer {
	log.Printf("Client %s joining room %s", msg.ClientID, msg.Room)

	pc, err := createPeerConnection()
	if err != nil {
		log.Printf("Failed to create PeerConnection: %v", err)
		return nil
	}

	peer := &Peer{
		ID:             msg.ClientID,
		Conn:           conn,
		PeerConnection: pc,
		LocalTracks:    make(map[string]*webrtc.TrackLocalStaticRTP),
	}

	room := roomManager.GetOrCreateRoom(msg.Room)

	// Notify existing peers about new peer
	room.BroadcastExcept(peer.ID, SignalMessage{
		Type:     "peer_joined",
		ClientID: peer.ID,
	})

	room.AddPeer(peer)

	// Set up ICE candidate handling
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		peer.SendMessage(SignalMessage{
			Type:      "candidate",
			Candidate: candidate.ToJSON().Candidate,
		})
	})

	// Handle incoming tracks (audio from this peer)
	pc.OnTrack(func(remoteTrack *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Printf("Received track from %s: %s", peer.ID, remoteTrack.Codec().MimeType)

		// Create a local track for forwarding to other peers
		localTrack, err := webrtc.NewTrackLocalStaticRTP(
			remoteTrack.Codec().RTPCodecCapability,
			fmt.Sprintf("audio-%s", peer.ID),
			fmt.Sprintf("stream-%s", peer.ID),
		)
		if err != nil {
			log.Printf("Failed to create local track: %v", err)
			return
		}

		peer.mu.Lock()
		peer.LocalTracks[remoteTrack.ID()] = localTrack
		peer.mu.Unlock()

		// Add this track to all other peers in the room
		for _, otherPeer := range room.GetOtherPeers(peer.ID) {
			addTrackToPeer(otherPeer, localTrack, peer.ID)
		}

		// Forward RTP packets from remote track to local track
		go func() {
			buf := make([]byte, 1500)
			for {
				n, _, err := remoteTrack.Read(buf)
				if err != nil {
					log.Printf("Track read error for %s: %v", peer.ID, err)
					return
				}
				if _, err := localTrack.Write(buf[:n]); err != nil {
					return
				}
			}
		}()
	})

	// Handle connection state changes
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Peer %s connection state: %s", peer.ID, state.String())
		if state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateClosed ||
			state == webrtc.PeerConnectionStateDisconnected {
			handlePeerDisconnect(peer)
		}
	})

	// Add tracks from existing peers to the new peer
	for _, existingPeer := range room.GetOtherPeers(peer.ID) {
		existingPeer.mu.Lock()
		for _, track := range existingPeer.LocalTracks {
			addTrackToPeer(peer, track, existingPeer.ID)
		}
		existingPeer.mu.Unlock()
	}

	// Add a transceiver to receive audio from this peer
	_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	if err != nil {
		log.Printf("Failed to add transceiver for %s: %v", peer.ID, err)
	}

	// Send initial offer to establish connection
	triggerNegotiation(peer)

	return peer
}

// handleOffer handles an SDP offer from a peer
func handleOffer(peer *Peer, msg SignalMessage) {
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  msg.SDP,
	}

	if err := peer.PeerConnection.SetRemoteDescription(offer); err != nil {
		log.Printf("Failed to set remote description for %s: %v", peer.ID, err)
		return
	}

	answer, err := peer.PeerConnection.CreateAnswer(nil)
	if err != nil {
		log.Printf("Failed to create answer for %s: %v", peer.ID, err)
		return
	}

	if err := peer.PeerConnection.SetLocalDescription(answer); err != nil {
		log.Printf("Failed to set local description for %s: %v", peer.ID, err)
		return
	}

	peer.SendMessage(SignalMessage{
		Type: "answer",
		SDP:  answer.SDP,
	})
}

// handleAnswer handles an SDP answer from a peer
func handleAnswer(peer *Peer, msg SignalMessage) {
	answer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  msg.SDP,
	}

	if err := peer.PeerConnection.SetRemoteDescription(answer); err != nil {
		log.Printf("Failed to set remote description for %s: %v", peer.ID, err)
	}
}

// handleCandidate handles an ICE candidate from a peer
func handleCandidate(peer *Peer, msg SignalMessage) {
	candidate := webrtc.ICECandidateInit{
		Candidate: msg.Candidate,
	}

	if err := peer.PeerConnection.AddICECandidate(candidate); err != nil {
		log.Printf("Failed to add ICE candidate for %s: %v", peer.ID, err)
	}
}

// handleScreenshot handles forwarding a screenshot to a target peer
func handleScreenshot(peer *Peer, msg SignalMessage) {
	if peer.Room == nil || msg.TargetID == "" {
		log.Printf("Screenshot from %s: missing room or target", peer.ID)
		return
	}

	// Find target peer in the same room
	targetPeer := peer.Room.GetPeer(msg.TargetID)
	if targetPeer == nil {
		log.Printf("Screenshot from %s: target peer %s not found", peer.ID, msg.TargetID)
		return
	}

	// Forward the screenshot to the target peer
	log.Printf("Forwarding screenshot from %s to %s (%d bytes)", peer.ID, msg.TargetID, len(msg.Data))
	targetPeer.SendMessage(SignalMessage{
		Type:     "screenshot",
		ClientID: peer.ID,
		Data:     msg.Data,
	})
}

// handlePeerDisconnect handles cleanup when a peer disconnects
func handlePeerDisconnect(peer *Peer) {
	if peer.Room != nil {
		peer.Room.RemovePeer(peer.ID)
		peer.Room.BroadcastExcept(peer.ID, SignalMessage{
			Type:     "peer_left",
			ClientID: peer.ID,
		})
	}

	if peer.PeerConnection != nil {
		peer.PeerConnection.Close()
	}

	log.Printf("Peer %s disconnected", peer.ID)
}
