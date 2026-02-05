package main

import (
	"log"

	"github.com/pion/webrtc/v4"
)

// createPeerConnection creates a new WebRTC peer connection with Opus audio support
func createPeerConnection() (*webrtc.PeerConnection, error) {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

	// Create MediaEngine with Opus support only (audio-focused)
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   48000,
			Channels:    2,
			SDPFmtpLine: "minptime=10;useinbandfec=1",
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine))
	return api.NewPeerConnection(config)
}

// triggerNegotiation creates and sends an offer to the peer
func triggerNegotiation(peer *Peer) {
	offer, err := peer.PeerConnection.CreateOffer(nil)
	if err != nil {
		log.Printf("Failed to create offer for %s: %v", peer.ID, err)
		return
	}

	if err := peer.PeerConnection.SetLocalDescription(offer); err != nil {
		log.Printf("Failed to set local description for %s: %v", peer.ID, err)
		return
	}

	peer.SendMessage(SignalMessage{
		Type: "offer",
		SDP:  offer.SDP,
	})
}
