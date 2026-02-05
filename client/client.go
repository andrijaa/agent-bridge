package client

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// SignalMessage represents a signaling message between client and server
type SignalMessage struct {
	Type      string `json:"type"`
	Room      string `json:"room,omitempty"`
	ClientID  string `json:"client_id,omitempty"`
	SDP       string `json:"sdp,omitempty"`
	Candidate string `json:"candidate,omitempty"`
	Data      string `json:"data,omitempty"`      // For screenshot base64 data
	TargetID  string `json:"target_id,omitempty"` // Target peer for screenshot
}

// AudioCallback is called when audio is received from another peer
type AudioCallback func(peerID string, track *webrtc.TrackRemote)

// PeerEventCallback is called when peers join or leave
type PeerEventCallback func(peerID string, joined bool)

// ScreenshotCallback is called when a screenshot is received from another peer
type ScreenshotCallback func(peerID string, imageData string)

// Client represents an audio bridge client
type Client struct {
	ID             string
	ServerURL      string
	Room           string
	conn           *websocket.Conn
	peerConnection *webrtc.PeerConnection
	audioTrack     *webrtc.TrackLocalStaticRTP
	onAudio        AudioCallback
	onPeerEvent    PeerEventCallback
	onScreenshot   ScreenshotCallback
	mu             sync.Mutex
	writeMu        sync.Mutex // separate mutex for WebSocket writes
	rtpMu          sync.Mutex // mutex for RTP writing
	connected      bool
	done           chan struct{}
	// RTP state for outgoing audio
	rtpSeqNum    uint16
	rtpTimestamp uint32
}

// NewClient creates a new audio bridge client
func NewClient(id, serverURL string) *Client {
	return &Client{
		ID:        id,
		ServerURL: serverURL,
		done:      make(chan struct{}),
	}
}

// OnAudioReceived sets the callback for received audio tracks
func (c *Client) OnAudioReceived(callback AudioCallback) {
	c.onAudio = callback
}

// OnPeerEvent sets the callback for peer join/leave events
func (c *Client) OnPeerEvent(callback PeerEventCallback) {
	c.onPeerEvent = callback
}

// OnScreenshotReceived sets the callback for received screenshots
func (c *Client) OnScreenshotReceived(callback ScreenshotCallback) {
	c.onScreenshot = callback
}

// Connect establishes connection to the server and joins a room
func (c *Client) Connect(room string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return fmt.Errorf("already connected")
	}

	c.Room = room

	// Connect to WebSocket
	conn, _, err := websocket.DefaultDialer.Dial(c.ServerURL, nil)
	if err != nil {
		return fmt.Errorf("websocket dial failed: %w", err)
	}
	c.conn = conn

	// Create PeerConnection
	pc, err := c.createPeerConnection()
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to create peer connection: %w", err)
	}
	c.peerConnection = pc

	// Create audio track for sending
	audioTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   48000,
			Channels:    2,
			SDPFmtpLine: "minptime=10;useinbandfec=1",
		},
		fmt.Sprintf("audio-%s", c.ID),
		fmt.Sprintf("stream-%s", c.ID),
	)
	if err != nil {
		pc.Close()
		conn.Close()
		return fmt.Errorf("failed to create audio track: %w", err)
	}
	c.audioTrack = audioTrack

	// Add the track to the peer connection
	sender, err := pc.AddTrack(audioTrack)
	if err != nil {
		pc.Close()
		conn.Close()
		return fmt.Errorf("failed to add track: %w", err)
	}

	// Read and discard RTCP packets
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buf); err != nil {
				return
			}
		}
	}()

	// Set up ICE candidate handling
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		c.sendMessage(SignalMessage{
			Type:      "candidate",
			Candidate: candidate.ToJSON().Candidate,
		})
	})

	// Handle incoming tracks
	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Printf("[%s] Received audio track: %s", c.ID, track.ID())
		if c.onAudio != nil {
			// Extract peer ID from track ID (format: audio-peerID)
			peerID := track.StreamID()
			if len(peerID) > 7 && peerID[:7] == "stream-" {
				peerID = peerID[7:]
			}
			go c.onAudio(peerID, track)
		}
	})

	// Handle connection state
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("[%s] Connection state: %s", c.ID, state.String())
	})

	// Start message handler
	go c.handleMessages()

	// Join the room - server will send offer after we join
	c.sendMessage(SignalMessage{
		Type:     "join",
		Room:     room,
		ClientID: c.ID,
	})

	c.connected = true
	log.Printf("[%s] Connected to room %s", c.ID, room)

	return nil
}

func (c *Client) createPeerConnection() (*webrtc.PeerConnection, error) {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

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

func (c *Client) handleMessages() {
	for {
		select {
		case <-c.done:
			return
		default:
		}

		var msg SignalMessage
		if err := c.conn.ReadJSON(&msg); err != nil {
			log.Printf("[%s] Read error: %v", c.ID, err)
			return
		}

		switch msg.Type {
		case "offer":
			c.handleOffer(msg)
		case "answer":
			c.handleAnswer(msg)
		case "candidate":
			c.handleCandidate(msg)
		case "peer_joined":
			log.Printf("[%s] Peer joined: %s", c.ID, msg.ClientID)
			if c.onPeerEvent != nil {
				c.onPeerEvent(msg.ClientID, true)
			}
		case "peer_left":
			log.Printf("[%s] Peer left: %s", c.ID, msg.ClientID)
			if c.onPeerEvent != nil {
				c.onPeerEvent(msg.ClientID, false)
			}
		case "screenshot":
			log.Printf("[%s] Screenshot received from: %s (%d bytes)", c.ID, msg.ClientID, len(msg.Data))
			if c.onScreenshot != nil {
				c.onScreenshot(msg.ClientID, msg.Data)
			}
		}
	}
}

func (c *Client) handleOffer(msg SignalMessage) {
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  msg.SDP,
	}

	if err := c.peerConnection.SetRemoteDescription(offer); err != nil {
		log.Printf("[%s] Failed to set remote description: %v", c.ID, err)
		return
	}

	answer, err := c.peerConnection.CreateAnswer(nil)
	if err != nil {
		log.Printf("[%s] Failed to create answer: %v", c.ID, err)
		return
	}

	if err := c.peerConnection.SetLocalDescription(answer); err != nil {
		log.Printf("[%s] Failed to set local description: %v", c.ID, err)
		return
	}

	c.sendMessage(SignalMessage{
		Type: "answer",
		SDP:  answer.SDP,
	})
}

func (c *Client) handleAnswer(msg SignalMessage) {
	answer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  msg.SDP,
	}

	if err := c.peerConnection.SetRemoteDescription(answer); err != nil {
		log.Printf("[%s] Failed to set remote description: %v", c.ID, err)
	}
}

func (c *Client) handleCandidate(msg SignalMessage) {
	candidate := webrtc.ICECandidateInit{
		Candidate: msg.Candidate,
	}

	if err := c.peerConnection.AddICECandidate(candidate); err != nil {
		log.Printf("[%s] Failed to add ICE candidate: %v", c.ID, err)
	}
}

func (c *Client) sendMessage(msg SignalMessage) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteJSON(msg)
}

// WriteRTP writes a raw RTP packet to the audio track
func (c *Client) WriteRTP(data []byte) error {
	if c.audioTrack == nil {
		return fmt.Errorf("audio track not initialized")
	}
	_, err := c.audioTrack.Write(data)
	return err
}

// WriteOpus writes an Opus payload with proper RTP headers
func (c *Client) WriteOpus(opusData []byte) error {
	if c.audioTrack == nil {
		return fmt.Errorf("audio track not initialized")
	}

	c.rtpMu.Lock()
	seqNum := c.rtpSeqNum
	timestamp := c.rtpTimestamp
	c.rtpSeqNum++
	c.rtpTimestamp += 960 // 20ms at 48kHz
	c.rtpMu.Unlock()

	packet := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			Padding:        false,
			Extension:      false,
			Marker:         false,
			PayloadType:    111, // Opus
			SequenceNumber: seqNum,
			Timestamp:      timestamp,
			SSRC:           0x12345678, // Will be overwritten by pion
		},
		Payload: opusData,
	}

	return c.audioTrack.WriteRTP(packet)
}

// GetAudioTrack returns the local audio track for direct RTP writing
func (c *Client) GetAudioTrack() *webrtc.TrackLocalStaticRTP {
	return c.audioTrack
}

// Disconnect closes the connection
func (c *Client) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return nil
	}

	close(c.done)

	if c.peerConnection != nil {
		c.peerConnection.Close()
	}

	if c.conn != nil {
		c.conn.Close()
	}

	c.connected = false
	log.Printf("[%s] Disconnected", c.ID)
	return nil
}

// IsConnected returns whether the client is connected
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// AudioReader is a helper to read audio from a track
type AudioReader struct {
	Track  *webrtc.TrackRemote
	PeerID string
}

// ReadRTP reads an RTP packet from the track
func (ar *AudioReader) ReadRTP() ([]byte, error) {
	buf := make([]byte, 1500)
	n, _, err := ar.Track.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// SimpleAudioGenerator generates test audio (silence with periodic markers)
type SimpleAudioGenerator struct {
	sampleRate uint32
	channels   uint16
	frameSize  int
	seqNum     uint16
	timestamp  uint32
}

// NewSimpleAudioGenerator creates a new audio generator for testing
func NewSimpleAudioGenerator() *SimpleAudioGenerator {
	return &SimpleAudioGenerator{
		sampleRate: 48000,
		channels:   2,
		frameSize:  960, // 20ms at 48kHz
	}
}

// GenerateFrame generates a test audio frame (Opus-like RTP packet)
func (g *SimpleAudioGenerator) GenerateFrame() []byte {
	// Create a minimal RTP-like packet header + payload
	// In real usage, you'd use an Opus encoder
	packet := make([]byte, 12+3) // RTP header (12) + minimal Opus frame

	// RTP header
	packet[0] = 0x80                           // Version 2
	packet[1] = 111                            // Payload type (Opus)
	packet[2] = byte(g.seqNum >> 8)            // Sequence number
	packet[3] = byte(g.seqNum)                 // Sequence number
	packet[4] = byte(g.timestamp >> 24)        // Timestamp
	packet[5] = byte(g.timestamp >> 16)        // Timestamp
	packet[6] = byte(g.timestamp >> 8)         // Timestamp
	packet[7] = byte(g.timestamp)              // Timestamp
	packet[8] = 0                              // SSRC (would be set by WebRTC)
	packet[9] = 0                              // SSRC
	packet[10] = 0                             // SSRC
	packet[11] = 1                             // SSRC
	packet[12] = 0xFC                          // Opus TOC byte (silence frame)
	packet[13] = 0xFF                          // Opus frame
	packet[14] = 0xFE                          // Opus frame

	g.seqNum++
	g.timestamp += uint32(g.frameSize)

	return packet
}

// StartGenerating starts generating audio frames at the correct interval
func (g *SimpleAudioGenerator) StartGenerating(client *Client, done chan struct{}) {
	ticker := time.NewTicker(20 * time.Millisecond) // 20ms Opus frames
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			frame := g.GenerateFrame()
			if err := client.WriteRTP(frame); err != nil {
				log.Printf("Write error: %v", err)
				return
			}
		}
	}
}

// Helper function to marshal SignalMessage to JSON
func (m SignalMessage) ToJSON() string {
	b, _ := json.Marshal(m)
	return string(b)
}
