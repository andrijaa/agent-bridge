package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const apiURL = "https://api.openai.com/v1/chat/completions"

// Message represents a chat message
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ContentPart represents a part of a vision message content
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL represents an image URL in a vision message
type ImageURL struct {
	URL    string `json:"url"`    // "data:image/jpeg;base64,..."
	Detail string `json:"detail"` // "low", "high", or "auto"
}

// VisionMessage represents a chat message with image content
type VisionMessage struct {
	Role    string        `json:"role"`
	Content []ContentPart `json:"content"`
}

// Client is an OpenAI API client
type Client struct {
	apiKey       string
	model        string
	visionModel  string
	systemPrompt string
	httpClient   *http.Client
	messages     []Message // Conversation history
}

// Config holds OpenAI client configuration
type Config struct {
	APIKey       string
	Model        string // e.g., "gpt-4o-mini" for text (default)
	VisionModel  string // e.g., "gpt-4o" for vision (default)
	SystemPrompt string
}

// StreamCallback is called for each chunk of the streaming response
type StreamCallback func(chunk string, done bool)

// NewClient creates a new OpenAI client
func NewClient(config Config) *Client {
	if config.Model == "" {
		config.Model = "gpt-4o-mini" // Cost-optimized for text
	}
	if config.VisionModel == "" {
		config.VisionModel = "gpt-4o" // Required for vision/images
	}
	if config.SystemPrompt == "" {
		config.SystemPrompt = "You are a helpful voice assistant. Keep responses concise and conversational since they will be spoken aloud. Respond in 1-2 sentences."
	}

	return &Client{
		apiKey:       config.APIKey,
		model:        config.Model,
		visionModel:  config.VisionModel,
		systemPrompt: config.SystemPrompt,
		httpClient:   &http.Client{},
	}
}

// chatRequest is the request body for chat completions
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []interface{} `json:"messages"`
	Stream   bool          `json:"stream"`
}

// streamResponse represents a streaming response chunk
type streamResponse struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// ChatStream sends a message and streams the response
func (c *Client) ChatStream(userMessage string, callback StreamCallback) error {
	return c.ChatStreamWithContext(context.Background(), userMessage, callback)
}

// ChatStreamWithContext sends a message and streams the response with context support
// Maintains conversation history for multi-turn conversations
func (c *Client) ChatStreamWithContext(ctx context.Context, userMessage string, callback StreamCallback) error {
	// Add user message to history
	c.messages = append(c.messages, Message{Role: "user", Content: userMessage})

	// Build messages array with system prompt + conversation history
	messages := make([]interface{}, 0, len(c.messages)+1)
	messages = append(messages, Message{Role: "system", Content: c.systemPrompt})
	for _, msg := range c.messages {
		messages = append(messages, msg)
	}

	reqBody := chatRequest{
		Model:    c.model,
		Messages: messages,
		Stream:   true,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	// Read SSE stream
	reader := bufio.NewReader(resp.Body)
	var fullResponse strings.Builder

	for {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read error: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			callback(fullResponse.String(), true)
			break
		}

		var streamResp streamResponse
		if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
			continue
		}

		if len(streamResp.Choices) > 0 {
			content := streamResp.Choices[0].Delta.Content
			if content != "" {
				fullResponse.WriteString(content)
				callback(content, false)
			}
		}
	}

	// Add assistant response to history
	if fullResponse.Len() > 0 {
		c.messages = append(c.messages, Message{Role: "assistant", Content: fullResponse.String()})
	}

	return nil
}

// Chat sends a message and returns the complete response (non-streaming)
func (c *Client) Chat(userMessage string) (string, error) {
	var response strings.Builder

	err := c.ChatStream(userMessage, func(chunk string, done bool) {
		if done {
			response.Reset()
			response.WriteString(chunk)
		}
	})

	if err != nil {
		return "", err
	}

	return response.String(), nil
}

// ClearHistory clears the conversation history
func (c *Client) ClearHistory() {
	c.messages = nil
}

// GetMessages returns a copy of the conversation history
func (c *Client) GetMessages() []Message {
	result := make([]Message, len(c.messages))
	copy(result, c.messages)
	return result
}

// MessageCount returns the number of messages in history
func (c *Client) MessageCount() int {
	return len(c.messages)
}

// ChatStreamWithImage sends a message with an image and streams the response
// Maintains conversation history for multi-turn conversations
func (c *Client) ChatStreamWithImage(ctx context.Context, userMessage, imageBase64 string, callback StreamCallback) error {
	// Add user message to history (text only, we don't store images in history)
	c.messages = append(c.messages, Message{Role: "user", Content: userMessage + " [with screenshot]"})

	// Build vision message with both text and image
	userContent := []ContentPart{
		{Type: "text", Text: userMessage},
		{
			Type: "image_url",
			ImageURL: &ImageURL{
				URL:    "data:image/jpeg;base64," + imageBase64,
				Detail: "low", // Use "low" for faster/cheaper processing
			},
		},
	}

	// Build messages array with system prompt + history + current vision message
	messages := make([]interface{}, 0, len(c.messages)+1)
	messages = append(messages, Message{Role: "system", Content: c.systemPrompt})
	// Add previous messages (excluding the one we just added)
	for i := 0; i < len(c.messages)-1; i++ {
		messages = append(messages, c.messages[i])
	}
	// Add current vision message with image
	messages = append(messages, VisionMessage{Role: "user", Content: userContent})

	// Use vision model for image requests (gpt-4o by default)
	reqBody := chatRequest{
		Model:    c.visionModel,
		Messages: messages,
		Stream:   true,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	// Read SSE stream
	reader := bufio.NewReader(resp.Body)
	var fullResponse strings.Builder

	for {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read error: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			callback(fullResponse.String(), true)
			break
		}

		var streamResp streamResponse
		if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
			continue
		}

		if len(streamResp.Choices) > 0 {
			content := streamResp.Choices[0].Delta.Content
			if content != "" {
				fullResponse.WriteString(content)
				callback(content, false)
			}
		}
	}

	// Add assistant response to history
	if fullResponse.Len() > 0 {
		c.messages = append(c.messages, Message{Role: "assistant", Content: fullResponse.String()})
	}

	return nil
}
