package audio

import (
	"encoding/binary"
	"fmt"

	"gopkg.in/hraban/opus.v2"
)

// OpusEncoder encodes PCM audio to Opus
type OpusEncoder struct {
	encoder    *opus.Encoder
	sampleRate int
	channels   int
	frameSize  int // samples per channel per frame
}

// NewOpusEncoder creates a new Opus encoder
func NewOpusEncoder(sampleRate, channels, frameSize int) (*OpusEncoder, error) {
	enc, err := opus.NewEncoder(sampleRate, channels, opus.AppVoIP)
	if err != nil {
		return nil, err
	}

	// Set bitrate for voice
	enc.SetBitrate(64000)

	return &OpusEncoder{
		encoder:    enc,
		sampleRate: sampleRate,
		channels:   channels,
		frameSize:  frameSize,
	}, nil
}

// Encode encodes PCM int16 samples to Opus
func (e *OpusEncoder) Encode(pcm []int16) ([]byte, error) {
	data := make([]byte, 1024)
	n, err := e.encoder.Encode(pcm, data)
	if err != nil {
		return nil, err
	}
	return data[:n], nil
}

// EncodeBytes encodes PCM bytes (little-endian int16) to Opus
func (e *OpusEncoder) EncodeBytes(pcmBytes []byte) ([]byte, error) {
	numSamples := len(pcmBytes) / 2
	pcm := make([]int16, numSamples)
	for i := 0; i < numSamples; i++ {
		pcm[i] = int16(binary.LittleEndian.Uint16(pcmBytes[i*2:]))
	}
	return e.Encode(pcm)
}

// FrameSize returns the frame size in samples per channel
func (e *OpusEncoder) FrameSize() int {
	return e.frameSize
}

// SampleRate returns the sample rate
func (e *OpusEncoder) SampleRate() int {
	return e.sampleRate
}

// Channels returns the number of channels
func (e *OpusEncoder) Channels() int {
	return e.channels
}

// ResampleMono resamples mono PCM from one sample rate to another
// Uses linear interpolation
func ResampleMono(input []byte, inputRate, outputRate int) []byte {
	if inputRate == outputRate {
		return input
	}

	inputSamples := len(input) / 2
	ratio := float64(outputRate) / float64(inputRate)
	outputSamples := int(float64(inputSamples) * ratio)

	output := make([]byte, outputSamples*2)

	for i := 0; i < outputSamples; i++ {
		// Calculate source position
		srcPos := float64(i) / ratio
		srcIdx := int(srcPos)
		frac := srcPos - float64(srcIdx)

		// Clamp indices
		idx1 := srcIdx
		idx2 := srcIdx + 1
		if idx1 >= inputSamples {
			idx1 = inputSamples - 1
		}
		if idx2 >= inputSamples {
			idx2 = inputSamples - 1
		}

		// Get samples
		s1 := int16(binary.LittleEndian.Uint16(input[idx1*2:]))
		s2 := int16(binary.LittleEndian.Uint16(input[idx2*2:]))

		// Linear interpolation
		sample := int16(float64(s1)*(1-frac) + float64(s2)*frac)

		binary.LittleEndian.PutUint16(output[i*2:], uint16(sample))
	}

	return output
}

// MonoToStereo converts mono PCM to stereo by duplicating each sample
func MonoToStereo(mono []byte) []byte {
	numSamples := len(mono) / 2
	stereo := make([]byte, numSamples*4) // 2 bytes per sample * 2 channels

	for i := 0; i < numSamples; i++ {
		sample := mono[i*2 : i*2+2]
		// Left channel
		stereo[i*4] = sample[0]
		stereo[i*4+1] = sample[1]
		// Right channel
		stereo[i*4+2] = sample[0]
		stereo[i*4+3] = sample[1]
	}

	return stereo
}

// RTPPacketizer creates RTP packets from Opus frames
type RTPPacketizer struct {
	ssrc       uint32
	seqNum     uint16
	timestamp  uint32
	sampleRate uint32
	frameSize  uint32
}

// NewRTPPacketizer creates a new RTP packetizer
func NewRTPPacketizer(ssrc uint32, sampleRate, frameSize int) *RTPPacketizer {
	return &RTPPacketizer{
		ssrc:       ssrc,
		seqNum:     0,
		timestamp:  0,
		sampleRate: uint32(sampleRate),
		frameSize:  uint32(frameSize),
	}
}

// Packetize creates an RTP packet from Opus data
func (p *RTPPacketizer) Packetize(opusData []byte) []byte {
	packet := make([]byte, 12+len(opusData))

	// RTP header
	packet[0] = 0x80              // Version 2
	packet[1] = 111               // Payload type (Opus)
	packet[2] = byte(p.seqNum >> 8)
	packet[3] = byte(p.seqNum)
	packet[4] = byte(p.timestamp >> 24)
	packet[5] = byte(p.timestamp >> 16)
	packet[6] = byte(p.timestamp >> 8)
	packet[7] = byte(p.timestamp)
	packet[8] = byte(p.ssrc >> 24)
	packet[9] = byte(p.ssrc >> 16)
	packet[10] = byte(p.ssrc >> 8)
	packet[11] = byte(p.ssrc)

	copy(packet[12:], opusData)

	p.seqNum++
	p.timestamp += p.frameSize

	return packet
}

// AudioPipeline processes ElevenLabs audio for WebRTC
type AudioPipeline struct {
	encoder *OpusEncoder
	buffer  []byte // Buffer for accumulating PCM data
}

// NewAudioPipeline creates a pipeline to convert ElevenLabs audio to Opus
// ElevenLabs: 24kHz mono PCM -> WebRTC: 48kHz stereo Opus
func NewAudioPipeline() (*AudioPipeline, error) {
	// Opus encoder: 48kHz stereo, 20ms frames (960 samples per channel)
	encoder, err := NewOpusEncoder(48000, 2, 960)
	if err != nil {
		return nil, fmt.Errorf("failed to create encoder: %w", err)
	}

	return &AudioPipeline{
		encoder: encoder,
		buffer:  make([]byte, 0),
	}, nil
}

// ProcessChunk converts ElevenLabs PCM (22050Hz mono) to Opus payloads (48kHz stereo)
// Returns slice of Opus encoded frames ready to be sent via RTP
func (p *AudioPipeline) ProcessChunk(pcm22kMono []byte) ([][]byte, error) {
	if len(pcm22kMono) == 0 {
		return nil, nil
	}

	// Step 1: Resample 22050Hz -> 48kHz (mono)
	pcm48kMono := ResampleMono(pcm22kMono, 22050, 48000)

	// Step 2: Convert mono to stereo
	pcm48kStereo := MonoToStereo(pcm48kMono)

	// Step 3: Append to buffer
	p.buffer = append(p.buffer, pcm48kStereo...)

	// Step 4: Process complete frames
	// Frame size: 960 samples * 2 channels * 2 bytes = 3840 bytes
	frameBytes := 960 * 2 * 2
	var opusFrames [][]byte

	for len(p.buffer) >= frameBytes {
		frame := p.buffer[:frameBytes]
		p.buffer = p.buffer[frameBytes:]

		// Encode to Opus
		opusData, err := p.encoder.EncodeBytes(frame)
		if err != nil {
			continue
		}

		opusFrames = append(opusFrames, opusData)
	}

	return opusFrames, nil
}

// Flush processes any remaining buffered data (with padding if needed)
func (p *AudioPipeline) Flush() ([][]byte, error) {
	frameBytes := 960 * 2 * 2

	// Pad the buffer to frame boundary if needed
	if len(p.buffer) > 0 {
		padding := make([]byte, frameBytes-len(p.buffer)%frameBytes)
		p.buffer = append(p.buffer, padding...)

		// Process the padded data
		var opusFrames [][]byte
		for len(p.buffer) >= frameBytes {
			frame := p.buffer[:frameBytes]
			p.buffer = p.buffer[frameBytes:]

			opusData, err := p.encoder.EncodeBytes(frame)
			if err != nil {
				continue
			}
			opusFrames = append(opusFrames, opusData)
		}
		return opusFrames, nil
	}

	return nil, nil
}

// Reset clears the internal buffer
func (p *AudioPipeline) Reset() {
	p.buffer = p.buffer[:0]
}
