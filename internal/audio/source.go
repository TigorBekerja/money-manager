package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// Source delivers 16 kHz mono 16-bit audio in chunks. Frames is closed after Close or
// when the device fails (then Err says why).
type Source interface {
	Frames() <-chan []int16
	Err() error
	Close() error
}

// fileSource plays a WAV file as if it came from a microphone: in real time, followed by
// silence until closed. Used for development and end-to-end tests.
type fileSource struct {
	frames chan []int16
	stop   chan struct{}
	once   sync.Once
	done   chan struct{}
}

// OpenWAVFile reads a 16 kHz mono 16-bit PCM WAV. With realtime, chunks are paced like a
// live microphone.
func OpenWAVFile(path string, realtime bool) (Source, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	samples, err := DecodeWAV(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	f := &fileSource{frames: make(chan []int16, 8), stop: make(chan struct{}), done: make(chan struct{})}
	go f.run(samples, realtime)
	return f, nil
}

func (f *fileSource) run(samples []int16, realtime bool) {
	defer close(f.done)
	defer close(f.frames)
	const chunk = SampleRate / 10 // 100 ms
	silence := make([]int16, chunk)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for i := 0; ; i += chunk {
		var c []int16
		if i < len(samples) {
			c = samples[i:min(i+chunk, len(samples))]
		} else {
			c = silence
		}
		if realtime {
			select {
			case <-tick.C:
			case <-f.stop:
				return
			}
		}
		select {
		case f.frames <- c:
		case <-f.stop:
			return
		}
	}
}

func (f *fileSource) Frames() <-chan []int16 { return f.frames }
func (f *fileSource) Err() error             { return nil }
func (f *fileSource) Close() error {
	f.once.Do(func() { close(f.stop) })
	<-f.done
	return nil
}

// DecodeWAV reads mono 16-bit PCM at SampleRate.
func DecodeWAV(data []byte) ([]int16, error) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, errors.New("not a WAV file")
	}
	le := binary.LittleEndian
	var fmtOK bool
	for p := 12; p+8 <= len(data); {
		id, size := string(data[p:p+4]), int(le.Uint32(data[p+4:]))
		body := data[p+8 : min(len(data), p+8+size)]
		switch id {
		case "fmt ":
			if len(body) < 16 {
				return nil, errors.New("bad WAV header")
			}
			format, channels := le.Uint16(body[0:]), le.Uint16(body[2:])
			rate, bits := le.Uint32(body[4:]), le.Uint16(body[14:])
			if format != 1 || channels != 1 || rate != SampleRate || bits != 16 {
				return nil, fmt.Errorf("need 16 kHz mono 16-bit PCM, got format %d, %d channels, %d Hz, %d bits",
					format, channels, rate, bits)
			}
			fmtOK = true
		case "data":
			if !fmtOK {
				return nil, errors.New("WAV data before format")
			}
			samples := make([]int16, len(body)/2)
			for i := range samples {
				samples[i] = int16(le.Uint16(body[2*i:]))
			}
			return samples, nil
		}
		p += 8 + size + size%2
	}
	return nil, errors.New("WAV file has no audio data")
}
