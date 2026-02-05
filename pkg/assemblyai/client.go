package assemblyai

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"example.com/agent_bridge/pkg/stt"
	"github.com/gorilla/websocket"
)

const (
	// Universal Streaming API endpoint
	assemblyWSURL = "wss://streaming.assemblyai.com/v3/ws"
)

// Client is an AssemblyAI Universal Streaming STT client
type Client struct {
	apiKey         string
	conn           *websocket.Conn
	callback       stt.TranscriptCallback
	utteranceEndCb stt.UtteranceEndCallback
	mu             sync.Mutex
	connected      bool
	done           chan struct{}
	sampleRate     int
	channels       int

	// Track the last transcript to detect new content
	lastTranscript string

	// Audio buffer for accumulating chunks (AssemblyAI requires 50-1000ms per send)
	audioBuffer []byte
}

// Config holds AssemblyAI connection settings
type Config struct {
	APIKey         string
	SampleRate     int // Input sample rate (e.g., 48000) - will be resampled to 16kHz
	Channels       int // e.g., 1 or 2
	UtteranceEndMs int // Not used - AssemblyAI handles endpointing automatically
}

// SessionBeginsMessage is sent when the session starts
type SessionBeginsMessage struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	ExpiresAt string `json:"expires_at"`
}

// TurnMessage represents AssemblyAI's Universal Streaming transcript response
type TurnMessage struct {
	Type                 string  `json:"type"`
	TurnOrder            int     `json:"turn_order"`
	Transcript           string  `json:"transcript"`
	Utterance            string  `json:"utterance"`
	EndOfTurn            bool    `json:"end_of_turn"`
	EndOfTurnConfidence  float64 `json:"end_of_turn_confidence"`
	LanguageCode         string  `json:"language_code,omitempty"`
	Words                []Word  `json:"words,omitempty"`
}

// Word represents word-level details in the transcript
type Word struct {
	Text       string  `json:"text"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Confidence float64 `json:"confidence"`
}

// TerminateMessage is sent to end the session
type TerminateMessage struct {
	Type bool `json:"terminate_session"`
}

// NewClient creates a new AssemblyAI client
func NewClient(config Config) *Client {
	if config.SampleRate == 0 {
		config.SampleRate = 48000
	}
	if config.Channels == 0 {
		config.Channels = 1
	}

	return &Client{
		apiKey:      config.APIKey,
		sampleRate:  config.SampleRate,
		channels:    config.Channels,
		done:        make(chan struct{}),
		audioBuffer: make([]byte, 0, minAudioBytes*2),
	}
}

// OnTranscript sets the callback for transcriptions
func (c *Client) OnTranscript(callback stt.TranscriptCallback) {
	c.callback = callback
}

// OnUtteranceEnd sets the callback for when the user finishes speaking
func (c *Client) OnUtteranceEnd(callback stt.UtteranceEndCallback) {
	c.utteranceEndCb = callback
}

// Connect establishes WebSocket connection to AssemblyAI Universal Streaming
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	// Universal Streaming uses query params for configuration
	// format_turns=true is required to receive Turn messages
	wsURL := fmt.Sprintf("%s?sample_rate=16000&format_turns=true", assemblyWSURL)

	// Connect with API key in Authorization header (just the key, no prefix)
	header := make(map[string][]string)
	header["Authorization"] = []string{c.apiKey}

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		return fmt.Errorf("assemblyai connection failed: %w", err)
	}

	c.conn = conn
	c.connected = true
	c.done = make(chan struct{})
	c.lastTranscript = ""
	c.audioBuffer = c.audioBuffer[:0] // Clear buffer

	// Start reading responses
	go c.readResponses()

	log.Println("[AssemblyAI] Connected to Universal Streaming service")
	return nil
}

func (c *Client) readResponses() {
	defer func() {
		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()
	}()

	for {
		select {
		case <-c.done:
			return
		default:
		}

		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				return
			}
			log.Printf("[AssemblyAI] Read error: %v", err)
			return
		}

		// Parse message type first
		var baseMsg struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(message, &baseMsg); err != nil {
			log.Printf("[AssemblyAI] Failed to parse message: %v", err)
			continue
		}

		switch baseMsg.Type {
		case "SessionBegins":
			var session SessionBeginsMessage
			if err := json.Unmarshal(message, &session); err == nil {
				log.Printf("[AssemblyAI] Session started: %s", session.SessionID)
			}

		case "Turn":
			var turn TurnMessage
			if err := json.Unmarshal(message, &turn); err != nil {
				log.Printf("[AssemblyAI] Failed to parse Turn: %v", err)
				continue
			}

			// Universal Streaming provides immutable transcripts
			// The transcript field contains all finalized words
			if turn.Transcript != "" && c.callback != nil {
				// Check if we have new content
				if turn.Transcript != c.lastTranscript {
					// Extract only the new portion
					newText := turn.Transcript
					if len(c.lastTranscript) > 0 && len(turn.Transcript) > len(c.lastTranscript) {
						newText = turn.Transcript[len(c.lastTranscript):]
					}

					c.lastTranscript = turn.Transcript

					// In Universal Streaming, transcripts are immutable (always "final")
					// but we use end_of_turn to signal utterance completion
					c.callback(newText, true)
				}
			}

			// Check for end of turn (user stopped speaking)
			if turn.EndOfTurn {
				log.Printf("[AssemblyAI] End of turn detected (confidence: %.2f)", turn.EndOfTurnConfidence)
				if c.utteranceEndCb != nil {
					c.utteranceEndCb()
				}
				// Reset for next utterance
				c.lastTranscript = ""
			}

		case "SessionTerminated":
			log.Println("[AssemblyAI] Session terminated")
			return

		case "Error":
			log.Printf("[AssemblyAI] Error: %s", string(message))

		default:
			// Ignore other message types
		}
	}
}

// minAudioBytes is the minimum audio chunk size to send (100ms at 16kHz mono = 3200 bytes)
// AssemblyAI requires between 50-1000ms per chunk
const minAudioBytes = 3200

// SendAudio sends PCM audio data to AssemblyAI
// Input is expected to be PCM s16le at the configured sample rate
// Will be resampled to 16kHz as required by AssemblyAI
// Audio is buffered and sent in chunks of at least 100ms
func (c *Client) SendAudio(pcmData []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.conn == nil {
		return fmt.Errorf("not connected")
	}

	// Convert bytes to int16 samples
	samples := make([]int16, len(pcmData)/2)
	for i := range samples {
		samples[i] = int16(pcmData[i*2]) | int16(pcmData[i*2+1])<<8
	}

	// If stereo, convert to mono by averaging channels
	if c.channels == 2 {
		monoSamples := make([]int16, len(samples)/2)
		for i := range monoSamples {
			left := int32(samples[i*2])
			right := int32(samples[i*2+1])
			monoSamples[i] = int16((left + right) / 2)
		}
		samples = monoSamples
	}

	// Resample from input rate to 16kHz
	resampled := resample(samples, c.sampleRate, 16000)

	// Convert back to bytes
	resampledBytes := make([]byte, len(resampled)*2)
	for i, s := range resampled {
		resampledBytes[i*2] = byte(s)
		resampledBytes[i*2+1] = byte(s >> 8)
	}

	// Buffer the audio
	c.audioBuffer = append(c.audioBuffer, resampledBytes...)

	// Only send when we have enough data (at least 100ms)
	if len(c.audioBuffer) >= minAudioBytes {
		err := c.conn.WriteMessage(websocket.BinaryMessage, c.audioBuffer)
		c.audioBuffer = c.audioBuffer[:0] // Clear buffer
		return err
	}

	return nil
}

// resample performs simple linear interpolation resampling
func resample(samples []int16, fromRate, toRate int) []int16 {
	if fromRate == toRate {
		return samples
	}

	ratio := float64(fromRate) / float64(toRate)
	outputLen := int(float64(len(samples)) / ratio)
	output := make([]int16, outputLen)

	for i := range output {
		srcIndex := float64(i) * ratio
		srcIndexInt := int(srcIndex)
		frac := srcIndex - float64(srcIndexInt)

		if srcIndexInt+1 < len(samples) {
			// Linear interpolation
			output[i] = int16(float64(samples[srcIndexInt])*(1-frac) + float64(samples[srcIndexInt+1])*frac)
		} else if srcIndexInt < len(samples) {
			output[i] = samples[srcIndexInt]
		}
	}

	return output
}

// Close closes the connection
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return nil
	}

	close(c.done)

	// Send terminate message
	if c.conn != nil {
		c.conn.WriteJSON(map[string]bool{"terminate_session": true})
		c.conn.Close()
	}

	c.connected = false
	log.Println("[AssemblyAI] Disconnected")
	return nil
}

// IsConnected returns connection status
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}
