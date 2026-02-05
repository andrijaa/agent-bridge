package stt

// TranscriptCallback is called when a transcript is received
type TranscriptCallback func(transcript string, isFinal bool)

// UtteranceEndCallback is called when the user finishes speaking
type UtteranceEndCallback func()

// Client defines the interface for speech-to-text providers
type Client interface {
	// OnTranscript sets the callback for transcriptions
	OnTranscript(callback TranscriptCallback)

	// OnUtteranceEnd sets the callback for when the user finishes speaking
	OnUtteranceEnd(callback UtteranceEndCallback)

	// Connect establishes connection to the STT service
	Connect() error

	// SendAudio sends PCM audio data to the STT service
	SendAudio(pcmData []byte) error

	// Close closes the connection
	Close() error

	// IsConnected returns connection status
	IsConnected() bool
}

// Config holds common STT connection settings
type Config struct {
	APIKey         string
	SampleRate     int    // e.g., 48000
	Channels       int    // e.g., 1 or 2
	UtteranceEndMs int    // Milliseconds of silence before utterance end
}
