// Package audio handles microphone access and audio encoding.
package audio

import (
	"bytes"
	"encoding/binary"
)

// EncodeWAV encodes mono 16-bit PCM samples as a WAV file.
func EncodeWAV(samples []int16, sampleRate int) []byte {
	const channels, bitsPerSample = 1, 16
	dataLen := len(samples) * 2
	var b bytes.Buffer
	b.Grow(44 + dataLen)

	w := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + dataLen))
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	w(uint32(16)) // PCM header size
	w(uint16(1))  // PCM format
	w(uint16(channels))
	w(uint32(sampleRate))
	w(uint32(sampleRate * channels * bitsPerSample / 8)) // byte rate
	w(uint16(channels * bitsPerSample / 8))              // block align
	w(uint16(bitsPerSample))
	b.WriteString("data")
	w(uint32(dataLen))
	w(samples)
	return b.Bytes()
}
