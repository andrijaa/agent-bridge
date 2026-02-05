package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"example.com/agent_bridge/client"
	"example.com/agent_bridge/pkg/assemblyai"
	"example.com/agent_bridge/pkg/audio"
	"example.com/agent_bridge/pkg/deepgram"
	"example.com/agent_bridge/pkg/elevenlabs"
	"example.com/agent_bridge/pkg/openai"
	"example.com/agent_bridge/pkg/stt"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// Persona represents an AI agent persona configuration
type Persona struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	VoiceID     string `json:"voice_id"`
	VoiceName   string `json:"voice_name"`
	Prompt      string `json:"prompt"`
}

// PromptsConfig holds all available personas
type PromptsConfig struct {
	Personas map[string]Persona `json:"personas"`
	Default  string             `json:"default"`
	Settings struct {
		MaxResponseSentences int  `json:"max_response_sentences"`
		AllowScreenContext   bool `json:"allow_screen_context"`
		ConversationMemory   bool `json:"conversation_memory"`
	} `json:"settings"`
}

// loadPromptsConfig loads persona configuration from JSON file
func loadPromptsConfig(configPath string) (*PromptsConfig, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var config PromptsConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return &config, nil
}

// getDefaultConfigPath returns the default path to prompts.json
func getDefaultConfigPath() string {
	// Try relative to executable first
	exe, err := os.Executable()
	if err == nil {
		configPath := filepath.Join(filepath.Dir(exe), "..", "..", "config", "prompts.json")
		if _, err := os.Stat(configPath); err == nil {
			return configPath
		}
	}

	// Try relative to working directory
	paths := []string{
		"config/prompts.json",
		"../config/prompts.json",
		"../../config/prompts.json",
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return "config/prompts.json"
}

// STTProvider indicates which speech-to-text provider to use
type STTProvider string

const (
	STTProviderDeepgram   STTProvider = "deepgram"
	STTProviderAssemblyAI STTProvider = "assemblyai"
)

// AIAgent represents a voice AI agent that can process and respond to audio
type AIAgent struct {
	ID               string
	PersonaName      string
	client           *client.Client
	sttClient        stt.Client
	sttProvider      STTProvider
	deepgramAPIKey   string
	assemblyAIAPIKey string
	openaiClient     *openai.Client
	elevenlabsClient *elevenlabs.Client
	audioPipeline    *audio.AudioPipeline
	activePeers      map[string]bool
	peersMu          sync.RWMutex
	audioReceived    int64
	audioSent        int64
	statsMu          sync.Mutex
	decoders         map[string]*audio.OpusDecoder
	decodersMu       sync.Mutex
	sttMu            sync.Mutex

	// Transcript accumulation
	transcriptMu       sync.Mutex
	pendingTranscript  strings.Builder
	lastTranscriptTime time.Time
	processingLLM      bool

	// Interruption handling
	speakingMu     sync.Mutex
	isSpeaking     bool
	cancelSpeaking chan struct{}
	cancelLLM      context.CancelFunc

	// Screenshot handling
	screenshotMu     sync.Mutex
	latestScreenshot string // base64 JPEG data
	screenshotPeerID string
}

// NewAIAgent creates a new AI agent with the specified persona
func NewAIAgent(id, serverURL, deepgramAPIKey, assemblyAIAPIKey, openaiAPIKey, elevenlabsAPIKey string, persona *Persona) *AIAgent {
	// Build the system prompt with screen context ability
	systemPrompt := persona.Prompt
	if !strings.Contains(strings.ToLower(systemPrompt), "screen") {
		// Add screen capability hint if not already in prompt
		systemPrompt += " You can also see the user's screen when they share it - reference what you see when relevant."
	}

	var oaiClient *openai.Client
	if openaiAPIKey != "" {
		oaiClient = openai.NewClient(openai.Config{
			APIKey:       openaiAPIKey,
			Model:        "gpt-4o-mini", // Cost-optimized for text
			VisionModel:  "gpt-4o",      // Auto-used when images are included
			SystemPrompt: systemPrompt,
		})
	}

	voiceID := persona.VoiceID
	if voiceID == "" {
		voiceID = "21m00Tcm4TlvDq8ikWAM" // Default to Rachel
	}

	var elevenClient *elevenlabs.Client
	if elevenlabsAPIKey != "" {
		elevenClient = elevenlabs.NewClient(elevenlabs.Config{
			APIKey:  elevenlabsAPIKey,
			VoiceID: voiceID,
			Model:   "eleven_turbo_v2_5",
		})
	}

	var pipeline *audio.AudioPipeline
	if elevenClient != nil {
		var err error
		pipeline, err = audio.NewAudioPipeline()
		if err != nil {
			log.Printf("Warning: Failed to create audio pipeline: %v", err)
		}
	}

	// Determine which STT provider to use based on which API key is provided
	var sttProvider STTProvider
	if assemblyAIAPIKey != "" {
		sttProvider = STTProviderAssemblyAI
	} else if deepgramAPIKey != "" {
		sttProvider = STTProviderDeepgram
	}

	return &AIAgent{
		ID:               id,
		PersonaName:      persona.Name,
		client:           client.NewClient(id, serverURL),
		sttProvider:      sttProvider,
		deepgramAPIKey:   deepgramAPIKey,
		assemblyAIAPIKey: assemblyAIAPIKey,
		openaiClient:     oaiClient,
		elevenlabsClient: elevenClient,
		audioPipeline:    pipeline,
		activePeers:      make(map[string]bool),
		decoders:         make(map[string]*audio.OpusDecoder),
	}
}

// handleTranscript processes transcripts from Deepgram
func (a *AIAgent) handleTranscript(transcript string, isFinal bool) {
	// Check if we should interrupt current speech
	a.speakingMu.Lock()
	speaking := a.isSpeaking
	a.speakingMu.Unlock()

	if speaking && transcript != "" {
		log.Printf("[%s] INTERRUPTION detected: %s", a.ID, transcript)
		a.interrupt()
	}

	a.transcriptMu.Lock()
	defer a.transcriptMu.Unlock()

	// Log the transcript
	marker := ""
	if isFinal {
		marker = "[FINAL]"
	}
	log.Printf("[%s] TRANSCRIPT %s %s", a.ID, marker, transcript)

	// Accumulate transcript (only final transcripts to avoid duplicates)
	if isFinal && transcript != "" {
		if a.pendingTranscript.Len() > 0 {
			a.pendingTranscript.WriteString(" ")
		}
		a.pendingTranscript.WriteString(transcript)
		a.lastTranscriptTime = time.Now()
	}

	// Don't trigger LLM here - wait for utterance end
}

// handleUtteranceEnd is called when Deepgram detects the user finished speaking
func (a *AIAgent) handleUtteranceEnd() {
	a.transcriptMu.Lock()
	defer a.transcriptMu.Unlock()

	if a.pendingTranscript.Len() == 0 || a.processingLLM {
		return
	}

	fullTranscript := a.pendingTranscript.String()
	a.pendingTranscript.Reset()
	a.processingLLM = true

	log.Printf("[%s] UTTERANCE END - processing: %s", a.ID, fullTranscript)

	// Process with LLM in background
	go a.processWithLLM(fullTranscript)
}

// interrupt stops current speech and cancels pending LLM request
func (a *AIAgent) interrupt() {
	// Stop current TTS playback
	a.speakingMu.Lock()
	if a.cancelSpeaking != nil {
		close(a.cancelSpeaking)
		a.cancelSpeaking = nil
	}
	a.speakingMu.Unlock()

	// Cancel pending LLM request
	if a.cancelLLM != nil {
		a.cancelLLM()
	}
}

// screenKeywords are words that indicate the user wants to discuss what's on screen
var screenKeywords = []string{
	"screen", "display", "showing", "see", "look", "looking",
	"watch", "watching", "view", "viewing", "monitor",
	"window", "browser", "app", "application",
	"what's this", "what is this", "what's that", "what is that",
	"show me", "tell me about", "describe",
}

// wantsScreenContext checks if the transcript suggests the user wants to discuss the screen
func wantsScreenContext(transcript string) bool {
	lower := strings.ToLower(transcript)
	for _, keyword := range screenKeywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

// processWithLLM sends transcript to OpenAI and speaks the response
func (a *AIAgent) processWithLLM(transcript string) {
	ctx, cancel := context.WithCancel(context.Background())
	a.cancelLLM = cancel

	defer func() {
		cancel()
		a.cancelLLM = nil
		a.transcriptMu.Lock()
		a.processingLLM = false
		a.transcriptMu.Unlock()
	}()

	if a.openaiClient == nil {
		return
	}

	log.Printf("[%s] USER: %s", a.ID, transcript)

	// Check if we have a screenshot and the user wants screen context
	a.screenshotMu.Lock()
	screenshot := a.latestScreenshot
	a.screenshotMu.Unlock()

	includeScreenshot := screenshot != "" && wantsScreenContext(transcript)

	// Collect the full response for TTS
	var fullResponse strings.Builder
	var err error

	if includeScreenshot {
		log.Printf("[%s] Including screenshot in LLM request (detected screen-related query)", a.ID)
		err = a.openaiClient.ChatStreamWithImage(ctx, transcript, screenshot, func(chunk string, done bool) {
			if !done {
				fullResponse.WriteString(chunk)
				fmt.Print(chunk) // Stream to console
			}
		})
	} else {
		err = a.openaiClient.ChatStreamWithContext(ctx, transcript, func(chunk string, done bool) {
			if !done {
				fullResponse.WriteString(chunk)
				fmt.Print(chunk) // Stream to console
			}
		})
	}
	fmt.Println()

	if err != nil {
		if ctx.Err() != nil {
			log.Printf("[%s] LLM request cancelled (interrupted)", a.ID)
			return
		}
		log.Printf("[%s] OpenAI error: %v", a.ID, err)
		return
	}

	responseText := fullResponse.String()
	log.Printf("[%s] ASSISTANT: %s", a.ID, responseText)

	// Convert to speech and send back
	if a.elevenlabsClient != nil && a.audioPipeline != nil && responseText != "" {
		a.speakResponse(responseText)
	}
}

// speakResponse converts text to speech and sends it via WebRTC
func (a *AIAgent) speakResponse(text string) {
	// Set up speaking state and cancellation channel
	a.speakingMu.Lock()
	a.isSpeaking = true
	a.cancelSpeaking = make(chan struct{})
	cancelCh := a.cancelSpeaking
	a.speakingMu.Unlock()

	defer func() {
		a.speakingMu.Lock()
		a.isSpeaking = false
		a.cancelSpeaking = nil
		a.speakingMu.Unlock()
	}()

	log.Printf("[%s] Speaking response...", a.ID)

	// Reset the pipeline buffer
	a.audioPipeline.Reset()

	// Get audio from ElevenLabs
	pcmData, err := a.elevenlabsClient.Synthesize(text)
	if err != nil {
		log.Printf("[%s] ElevenLabs error: %v", a.ID, err)
		return
	}

	log.Printf("[%s] Got %d bytes of PCM audio from ElevenLabs (22050Hz mono)", a.ID, len(pcmData))

	// Process through pipeline (resample, encode to Opus)
	opusFrames, err := a.audioPipeline.ProcessChunk(pcmData)
	if err != nil {
		log.Printf("[%s] Audio pipeline error: %v", a.ID, err)
		return
	}

	// Flush remaining samples
	flushFrames, _ := a.audioPipeline.Flush()
	opusFrames = append(opusFrames, flushFrames...)

	duration := float64(len(opusFrames)) * 20 / 1000 // seconds
	log.Printf("[%s] Sending %d Opus frames (%.1f seconds of audio)", a.ID, len(opusFrames), duration)

	// Send frames with proper timing (20ms per frame)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for i, opusData := range opusFrames {
		select {
		case <-cancelCh:
			log.Printf("[%s] Speech interrupted at frame %d/%d", a.ID, i, len(opusFrames))
			return
		case <-ticker.C:
			if err := a.client.WriteOpus(opusData); err != nil {
				log.Printf("[%s] Failed to send audio frame %d: %v", a.ID, i, err)
				return
			}

			a.statsMu.Lock()
			a.audioSent += int64(len(opusData))
			a.statsMu.Unlock()
		}
	}

	log.Printf("[%s] Finished speaking", a.ID)
}

// ensureSTTConnected connects to the configured STT provider if not already connected
func (a *AIAgent) ensureSTTConnected() error {
	a.sttMu.Lock()
	defer a.sttMu.Unlock()

	// No STT provider configured
	if a.sttProvider == "" {
		return nil
	}

	// Already connected
	if a.sttClient != nil && a.sttClient.IsConnected() {
		return nil
	}

	// Create client based on provider
	switch a.sttProvider {
	case STTProviderDeepgram:
		client := deepgram.NewClient(deepgram.Config{
			APIKey:         a.deepgramAPIKey,
			SampleRate:     48000,
			Channels:       2,
			UtteranceEndMs: 1000,
		})
		client.OnTranscript(a.handleTranscript)
		client.OnUtteranceEnd(a.handleUtteranceEnd)

		if err := client.Connect(); err != nil {
			log.Printf("[%s] Warning: Deepgram connection failed: %v", a.ID, err)
			return err
		}
		a.sttClient = client
		log.Printf("[%s] Using Deepgram for speech-to-text", a.ID)

	case STTProviderAssemblyAI:
		client := assemblyai.NewClient(assemblyai.Config{
			APIKey:         a.assemblyAIAPIKey,
			SampleRate:     48000,
			Channels:       2,
			UtteranceEndMs: 1000,
		})
		client.OnTranscript(a.handleTranscript)
		client.OnUtteranceEnd(a.handleUtteranceEnd)

		if err := client.Connect(); err != nil {
			log.Printf("[%s] Warning: AssemblyAI connection failed: %v", a.ID, err)
			return err
		}
		a.sttClient = client
		log.Printf("[%s] Using AssemblyAI for speech-to-text", a.ID)
	}

	return nil
}

// Start connects to the bridge and begins processing
func (a *AIAgent) Start(room string) error {
	// Set up audio callback
	a.client.OnAudioReceived(func(peerID string, track *webrtc.TrackRemote) {
		a.handleIncomingAudio(peerID, track)
	})

	// Set up peer event callback
	a.client.OnPeerEvent(func(peerID string, joined bool) {
		a.peersMu.Lock()
		defer a.peersMu.Unlock()

		if joined {
			a.activePeers[peerID] = true
			log.Printf("[%s] New peer connected: %s (total: %d)", a.ID, peerID, len(a.activePeers))
		} else {
			delete(a.activePeers, peerID)
			log.Printf("[%s] Peer disconnected: %s (total: %d)", a.ID, peerID, len(a.activePeers))
		}
	})

	// Set up screenshot callback
	a.client.OnScreenshotReceived(func(peerID string, imageData string) {
		a.screenshotMu.Lock()
		a.latestScreenshot = imageData
		a.screenshotPeerID = peerID
		a.screenshotMu.Unlock()
		log.Printf("[%s] Received screenshot from %s (%d bytes)", a.ID, peerID, len(imageData))
	})

	// Connect to the audio bridge
	if err := a.client.Connect(room); err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}

	log.Printf("[%s] AI Agent started in room: %s (persona: %s)", a.ID, room, a.PersonaName)
	return nil
}

// getOrCreateDecoder gets or creates an Opus decoder for a peer
func (a *AIAgent) getOrCreateDecoder(peerID string) (*audio.OpusDecoder, error) {
	a.decodersMu.Lock()
	defer a.decodersMu.Unlock()

	if dec, ok := a.decoders[peerID]; ok {
		return dec, nil
	}

	dec, err := audio.NewOpusDecoder(48000, 2)
	if err != nil {
		return nil, err
	}

	a.decoders[peerID] = dec
	return dec, nil
}

// handleIncomingAudio processes audio from other peers
func (a *AIAgent) handleIncomingAudio(peerID string, track *webrtc.TrackRemote) {
	log.Printf("[%s] Processing audio stream from: %s", a.ID, peerID)

	// Connect to STT provider when we start receiving audio
	if err := a.ensureSTTConnected(); err != nil {
		log.Printf("[%s] STT not available: %v", a.ID, err)
	}

	// Get or create decoder for this peer
	decoder, err := a.getOrCreateDecoder(peerID)
	if err != nil {
		log.Printf("[%s] Failed to create decoder for %s: %v", a.ID, peerID, err)
		return
	}

	buf := make([]byte, 1500)
	packet := &rtp.Packet{}

	for {
		n, _, err := track.Read(buf)
		if err != nil {
			log.Printf("[%s] Audio stream from %s ended: %v", a.ID, peerID, err)
			return
		}

		// Update stats
		a.statsMu.Lock()
		a.audioReceived += int64(n)
		a.statsMu.Unlock()

		// Parse RTP packet
		if err := packet.Unmarshal(buf[:n]); err != nil {
			continue
		}

		// Skip empty payloads
		if len(packet.Payload) == 0 {
			continue
		}

		// Decode Opus to PCM
		pcmBytes, err := decoder.DecodeToBytes(packet.Payload)
		if err != nil {
			continue
		}

		// Send to STT for transcription
		a.sttMu.Lock()
		sttClient := a.sttClient
		a.sttMu.Unlock()

		if sttClient != nil && sttClient.IsConnected() {
			if err := sttClient.SendAudio(pcmBytes); err != nil {
				log.Printf("[%s] STT send error: %v", a.ID, err)
			}
		}
	}
}

// SendAudio sends audio to all connected peers
func (a *AIAgent) SendAudio(data []byte) error {
	a.statsMu.Lock()
	a.audioSent += int64(len(data))
	a.statsMu.Unlock()

	return a.client.WriteRTP(data)
}

// StartTestAudio starts sending test audio for demonstration
func (a *AIAgent) StartTestAudio(done chan struct{}) {
	generator := client.NewSimpleAudioGenerator()
	go generator.StartGenerating(a.client, done)
}

// GetStats returns current audio statistics
func (a *AIAgent) GetStats() (received, sent int64, peers int) {
	a.statsMu.Lock()
	received = a.audioReceived
	sent = a.audioSent
	a.statsMu.Unlock()

	a.peersMu.RLock()
	peers = len(a.activePeers)
	a.peersMu.RUnlock()

	return
}

// Stop disconnects the agent
func (a *AIAgent) Stop() {
	if a.sttClient != nil {
		a.sttClient.Close()
	}
	a.client.Disconnect()
	log.Printf("[%s] AI Agent stopped", a.ID)
}

func main() {
	// Parse flags
	id := flag.String("id", "", "Agent ID (required)")
	room := flag.String("room", "ai-room", "Room to join")
	server := flag.String("server", "ws://localhost:8080/ws", "Server URL")
	sendTest := flag.Bool("test-audio", true, "Send test audio")
	deepgramKey := flag.String("deepgram-key", os.Getenv("DEEPGRAM_API_KEY"), "Deepgram API key (STT)")
	assemblyAIKey := flag.String("assemblyai-key", os.Getenv("ASSEMBLYAI_API_KEY"), "AssemblyAI API key (STT)")
	openaiKey := flag.String("openai-key", os.Getenv("OPENAI_API_KEY"), "OpenAI API key")
	elevenlabsKey := flag.String("elevenlabs-key", os.Getenv("ELEVENLABS_API_KEY"), "ElevenLabs API key")
	personaFlag := flag.String("persona", "", "Persona to use (see -list-personas)")
	listPersonas := flag.Bool("list-personas", false, "List available personas")
	configPath := flag.String("config", "", "Path to prompts.json config file")
	customPrompt := flag.String("prompt", "", "Custom system prompt (overrides persona)")
	flag.Parse()

	// Determine config path
	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = getDefaultConfigPath()
	}

	// Load prompts config
	promptsConfig, err := loadPromptsConfig(cfgPath)
	if err != nil {
		log.Printf("Warning: Could not load prompts config from %s: %v", cfgPath, err)
		// Use default persona if config fails
		promptsConfig = &PromptsConfig{
			Personas: map[string]Persona{
				"assistant": {
					Name:        "Helpful Assistant",
					Description: "A friendly, general-purpose voice assistant",
					VoiceID:     "21m00Tcm4TlvDq8ikWAM",
					VoiceName:   "Rachel",
					Prompt:      "You are a helpful voice assistant. Keep responses concise and conversational since they will be spoken aloud. Respond in 1-2 sentences maximum.",
				},
			},
			Default: "assistant",
		}
	}

	// Handle list-personas flag
	if *listPersonas {
		fmt.Println("\nAvailable Personas:")
		fmt.Println("==================")
		for key, persona := range promptsConfig.Personas {
			defaultMarker := ""
			if key == promptsConfig.Default {
				defaultMarker = " (default)"
			}
			fmt.Printf("\n  %s%s\n", key, defaultMarker)
			fmt.Printf("    Name:  %s\n", persona.Name)
			fmt.Printf("    Voice: %s\n", persona.VoiceName)
			fmt.Printf("    %s\n", persona.Description)
		}
		fmt.Println("\nUsage: go run main.go -id <agent-id> -persona <persona-key>")
		os.Exit(0)
	}

	if *id == "" {
		fmt.Println("Usage: go run main.go -id <agent-id> [options]")
		fmt.Println("\nOptions:")
		fmt.Println("  -room <room>              Room to join (default: ai-room)")
		fmt.Println("  -server <url>             Server URL (default: ws://localhost:8080/ws)")
		fmt.Println("  -persona <name>           Persona to use (see -list-personas)")
		fmt.Println("  -prompt <text>            Custom system prompt (overrides persona)")
		fmt.Println("  -config <path>            Path to prompts.json config file")
		fmt.Println("  -list-personas            Show all available personas")
		fmt.Println("  -deepgram-key <key>       Deepgram API key for STT (or DEEPGRAM_API_KEY env)")
		fmt.Println("  -assemblyai-key <key>     AssemblyAI API key for STT (or ASSEMBLYAI_API_KEY env)")
		fmt.Println("  -openai-key <key>         OpenAI API key (or OPENAI_API_KEY env)")
		fmt.Println("  -elevenlabs-key <key>     ElevenLabs API key (or ELEVENLABS_API_KEY env)")
		fmt.Println("  -test-audio=false         Disable test audio")
		fmt.Println("\nSTT Provider Selection:")
		fmt.Println("  If AssemblyAI key is provided, it will be used. Otherwise Deepgram is used.")
		fmt.Println("\nExample:")
		fmt.Println("  go run main.go -id assistant -persona technical")
		fmt.Println("  go run main.go -id coach -persona coach -room coaching")
		os.Exit(1)
	}

	// Select persona
	personaKey := *personaFlag
	if personaKey == "" {
		personaKey = promptsConfig.Default
	}

	persona, exists := promptsConfig.Personas[personaKey]
	if !exists {
		log.Fatalf("Unknown persona: %s. Use -list-personas to see available options.", personaKey)
	}

	// Override prompt if custom prompt provided
	if *customPrompt != "" {
		persona.Prompt = *customPrompt
		log.Printf("Using custom prompt (overriding %s persona)", personaKey)
	}

	log.Printf("Using persona: %s (%s) with voice: %s", personaKey, persona.Name, persona.VoiceName)

	// Determine STT provider
	if *assemblyAIKey != "" {
		log.Println("Using AssemblyAI for speech-to-text")
	} else if *deepgramKey != "" {
		log.Println("Using Deepgram for speech-to-text")
	} else {
		log.Println("Warning: No STT API key provided (DEEPGRAM_API_KEY or ASSEMBLYAI_API_KEY). Speech-to-text disabled.")
	}
	if *openaiKey == "" {
		log.Println("Warning: No OpenAI API key. LLM responses disabled.")
	}
	if *elevenlabsKey == "" {
		log.Println("Warning: No ElevenLabs API key. Text-to-speech disabled.")
	}

	// Create and start the AI agent
	agent := NewAIAgent(*id, *server, *deepgramKey, *assemblyAIKey, *openaiKey, *elevenlabsKey, &persona)

	if err := agent.Start(*room); err != nil {
		log.Fatalf("Failed to start agent: %v", err)
	}

	// Start test audio if enabled
	done := make(chan struct{})
	if *sendTest {
		log.Printf("[%s] Sending test audio (disable with -test-audio=false)", *id)
		agent.StartTestAudio(done)
	}

	// Print stats periodically
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				recv, sent, peers := agent.GetStats()
				log.Printf("[%s] Stats: %d peers | recv: %.1f KB | sent: %.1f KB",
					*id, peers, float64(recv)/1024, float64(sent)/1024)
			}
		}
	}()

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("[%s] Agent running. Press Ctrl+C to stop.", *id)
	<-sigChan

	// Cleanup
	close(done)
	agent.Stop()
}
