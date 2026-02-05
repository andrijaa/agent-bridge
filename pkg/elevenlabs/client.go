package elevenlabs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	apiURL = "https://api.elevenlabs.io/v1"
)

// Client is an ElevenLabs TTS client
type Client struct {
	apiKey  string
	voiceID string
	model   string
	client  *http.Client
}

// Config holds ElevenLabs client configuration
type Config struct {
	APIKey  string
	VoiceID string // e.g., "21m00Tcm4TlvDq8ikWAM" (Rachel)
	Model   string // e.g., "eleven_turbo_v2_5"
}

// AudioCallback is called with PCM audio chunks
type AudioCallback func(pcmData []byte)

// NewClient creates a new ElevenLabs client
func NewClient(config Config) *Client {
	if config.VoiceID == "" {
		config.VoiceID = "21m00Tcm4TlvDq8ikWAM" // Rachel - default voice
	}
	if config.Model == "" {
		config.Model = "eleven_turbo_v2_5" // Fast model
	}

	return &Client{
		apiKey:  config.APIKey,
		voiceID: config.VoiceID,
		model:   config.Model,
		client:  &http.Client{},
	}
}

// ttsRequest is the request body for text-to-speech
type ttsRequest struct {
	Text          string        `json:"text"`
	ModelID       string        `json:"model_id"`
	VoiceSettings voiceSettings `json:"voice_settings"`
}

type voiceSettings struct {
	Stability       float64 `json:"stability"`
	SimilarityBoost float64 `json:"similarity_boost"`
	Speed           float64 `json:"speed"`
}

// Synthesize converts text to speech and returns PCM audio (signed 16-bit LE, 22050Hz mono)
func (c *Client) Synthesize(text string) ([]byte, error) {
	// output_format must be a query parameter, not in the body
	// pcm_22050 = 22050Hz, 16-bit signed little-endian mono PCM
	url := fmt.Sprintf("%s/text-to-speech/%s?output_format=pcm_22050", apiURL, c.voiceID)

	reqBody := ttsRequest{
		Text:    text,
		ModelID: c.model,
		VoiceSettings: voiceSettings{
			Stability:       0.5,
			SimilarityBoost: 0.75,
			Speed:           1.0,
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("xi-api-key", c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	// Read all PCM data
	pcmData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return pcmData, nil
}

// SynthesizeStream converts text to speech and streams PCM audio chunks
func (c *Client) SynthesizeStream(text string, callback AudioCallback) error {
	url := fmt.Sprintf("%s/text-to-speech/%s/stream?output_format=pcm_22050", apiURL, c.voiceID)

	reqBody := ttsRequest{
		Text:    text,
		ModelID: c.model,
		VoiceSettings: voiceSettings{
			Stability:       0.5,
			SimilarityBoost: 0.75,
			Speed:           1.0,
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("xi-api-key", c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	// Stream PCM chunks
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			// Make a copy of the data for the callback
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			callback(chunk)
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read error: %w", err)
		}
	}

	return nil
}
