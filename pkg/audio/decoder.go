package audio

import (
	"encoding/binary"

	"gopkg.in/hraban/opus.v2"
)

// OpusDecoder decodes Opus audio to PCM
type OpusDecoder struct {
	decoder    *opus.Decoder
	sampleRate int
	channels   int
}

// NewOpusDecoder creates a new Opus decoder
func NewOpusDecoder(sampleRate, channels int) (*OpusDecoder, error) {
	dec, err := opus.NewDecoder(sampleRate, channels)
	if err != nil {
		return nil, err
	}

	return &OpusDecoder{
		decoder:    dec,
		sampleRate: sampleRate,
		channels:   channels,
	}, nil
}

// Decode decodes Opus data to PCM int16 samples
func (d *OpusDecoder) Decode(opusData []byte) ([]int16, error) {
	// Allocate buffer for decoded PCM (max frame size)
	// Opus can have frames of 2.5, 5, 10, 20, 40, or 60 ms
	// At 48kHz, 60ms = 2880 samples per channel
	pcm := make([]int16, 5760*d.channels)

	n, err := d.decoder.Decode(opusData, pcm)
	if err != nil {
		return nil, err
	}

	return pcm[:n*d.channels], nil
}

// DecodeToBytes decodes Opus to PCM bytes (little-endian int16)
func (d *OpusDecoder) DecodeToBytes(opusData []byte) ([]byte, error) {
	pcm, err := d.Decode(opusData)
	if err != nil {
		return nil, err
	}

	// Convert int16 to bytes
	bytes := make([]byte, len(pcm)*2)
	for i, sample := range pcm {
		binary.LittleEndian.PutUint16(bytes[i*2:], uint16(sample))
	}

	return bytes, nil
}

// SampleRate returns the sample rate
func (d *OpusDecoder) SampleRate() int {
	return d.sampleRate
}

// Channels returns the number of channels
func (d *OpusDecoder) Channels() int {
	return d.channels
}
