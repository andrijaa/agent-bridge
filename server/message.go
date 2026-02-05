package main

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
