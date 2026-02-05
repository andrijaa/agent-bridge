package deepgram

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
	deepgramWSURL = "wss://api.deepgram.com/v1/listen"
)

// Client is a Deepgram real-time STT client
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
	utteranceEndMs int
}

// Config holds Deepgram connection settings
type Config struct {
	APIKey         string
	SampleRate     int    // e.g., 48000
	Channels       int    // e.g., 1 or 2
	Encoding       string // "linear16" for PCM
	UtteranceEndMs int    // Milliseconds of silence before utterance end (default: 1000)
}

// MessageType is used to determine the type of Deepgram message
type MessageType struct {
	Type string `json:"type"`
}

// TranscriptResponse represents Deepgram's transcript response
type TranscriptResponse struct {
	Type    string `json:"type"`
	Channel struct {
		Alternatives []struct {
			Transcript string  `json:"transcript"`
			Confidence float64 `json:"confidence"`
		} `json:"alternatives"`
	} `json:"channel"`
	IsFinal bool `json:"is_final"`
}

// NewClient creates a new Deepgram client
func NewClient(config Config) *Client {
	if config.SampleRate == 0 {
		config.SampleRate = 48000
	}
	if config.Channels == 0 {
		config.Channels = 1
	}
	if config.UtteranceEndMs == 0 {
		config.UtteranceEndMs = 1000 // Default 1 second
	}

	return &Client{
		apiKey:         config.APIKey,
		sampleRate:     config.SampleRate,
		channels:       config.Channels,
		utteranceEndMs: config.UtteranceEndMs,
		done:           make(chan struct{}),
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

// Connect establishes WebSocket connection to Deepgram
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	// Build URL with query parameters
	url := fmt.Sprintf("%s?encoding=linear16&sample_rate=%d&channels=%d&punctuate=true&interim_results=true&utterance_end_ms=%d",
		deepgramWSURL, c.sampleRate, c.channels, c.utteranceEndMs)

	// Connect with API key header
	header := make(map[string][]string)
	header["Authorization"] = []string{"Token " + c.apiKey}

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.Dial(url, header)
	if err != nil {
		return fmt.Errorf("deepgram connection failed: %w", err)
	}

	c.conn = conn
	c.connected = true
	c.done = make(chan struct{})

	// Start reading responses
	go c.readResponses()

	log.Println("[Deepgram] Connected to speech-to-text service")
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
			log.Printf("[Deepgram] Read error: %v", err)
			return
		}

		// First, determine message type
		var msgType MessageType
		if err := json.Unmarshal(message, &msgType); err != nil {
			continue
		}

		// Handle utterance end - user finished speaking
		if msgType.Type == "UtteranceEnd" {
			log.Println("[Deepgram] Utterance end detected")
			if c.utteranceEndCb != nil {
				c.utteranceEndCb()
			}
			continue
		}

		// Handle transcript results
		if msgType.Type == "Results" {
			var resp TranscriptResponse
			if err := json.Unmarshal(message, &resp); err != nil {
				continue
			}
			if len(resp.Channel.Alternatives) > 0 {
				transcript := resp.Channel.Alternatives[0].Transcript
				if transcript != "" && c.callback != nil {
					c.callback(transcript, resp.IsFinal)
				}
			}
		}
	}
}

// SendAudio sends PCM audio data to Deepgram
func (c *Client) SendAudio(pcmData []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.conn == nil {
		return fmt.Errorf("not connected")
	}

	return c.conn.WriteMessage(websocket.BinaryMessage, pcmData)
}

// Close closes the connection
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return nil
	}

	close(c.done)

	// Send close message
	if c.conn != nil {
		c.conn.WriteMessage(websocket.TextMessage, []byte(`{"type": "CloseStream"}`))
		c.conn.Close()
	}

	c.connected = false
	log.Println("[Deepgram] Disconnected")
	return nil
}

// IsConnected returns connection status
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}
