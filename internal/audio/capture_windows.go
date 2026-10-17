//go:build windows

package audio

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procWaveInOpen       = winmm.NewProc("waveInOpen")
	procWaveInPrepare    = winmm.NewProc("waveInPrepareHeader")
	procWaveInUnprepare  = winmm.NewProc("waveInUnprepareHeader")
	procWaveInAddBuffer  = winmm.NewProc("waveInAddBuffer")
	procWaveInStart      = winmm.NewProc("waveInStart")
	procWaveInReset      = winmm.NewProc("waveInReset")
	procWaveInClose      = winmm.NewProc("waveInClose")
	procWaveInGetErrText = winmm.NewProc("waveInGetErrorTextW")
)

const (
	waveMapper    = 0xFFFFFFFF
	callbackEvent = 0x00050000
	whdrDone      = 0x00000001
	numBuffers    = 8
	bufferSamples = SampleRate / 10 // 100 ms per buffer
)

type waveFormatEx struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Size           uint16
}

// waveHdr mirrors WAVEHDR (64-bit layout).
type waveHdr struct {
	Data          uintptr
	BufferLength  uint32
	BytesRecorded uint32
	User          uintptr
	Flags         uint32
	Loops         uint32
	Next          uintptr
	Reserved      uintptr
}

type mic struct {
	handle  uintptr
	event   windows.Handle
	mem     windows.Handle // LocalAlloc'd headers + sample buffers: Windows writes into them
	headers []*waveHdr     // asynchronously, so they must not be Go-managed memory
	bufs    []*int16
	frames  chan []int16
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
	mu      sync.Mutex
	err     error
}

const hdrSize = unsafe.Sizeof(waveHdr{})

// OpenMic starts recording from the default Windows microphone at 16 kHz mono.
func OpenMic() (Source, error) {
	if err := procWaveInOpen.Find(); err != nil {
		return nil, fmt.Errorf("winmm.dll not available: %w", err)
	}
	ev, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return nil, err
	}
	m := &mic{event: ev, frames: make(chan []int16, 16), stop: make(chan struct{}), done: make(chan struct{})}

	format := waveFormatEx{FormatTag: 1, Channels: 1, SamplesPerSec: SampleRate,
		AvgBytesPerSec: SampleRate * 2, BlockAlign: 2, BitsPerSample: 16}
	r, _, _ := procWaveInOpen.Call(uintptr(unsafe.Pointer(&m.handle)), waveMapper,
		uintptr(unsafe.Pointer(&format)), uintptr(ev), 0, callbackEvent)
	if r != 0 {
		windows.CloseHandle(ev)
		return nil, fmt.Errorf("can't open the microphone: %s", mmError(r))
	}

	const bufBytes = bufferSamples * 2
	mem, err := windows.LocalAlloc(windows.LPTR, uint32((hdrSize+bufBytes)*numBuffers))
	if err != nil {
		procWaveInClose.Call(m.handle)
		windows.CloseHandle(ev)
		return nil, err
	}
	m.mem = windows.Handle(mem)
	base := *(*unsafe.Pointer)(unsafe.Pointer(&mem)) // non-Go memory from LocalAlloc
	for i := uintptr(0); i < numBuffers; i++ {
		h := (*waveHdr)(unsafe.Add(base, i*hdrSize))
		buf := unsafe.Add(base, numBuffers*hdrSize+i*bufBytes)
		h.Data = uintptr(buf)
		h.BufferLength = bufBytes
		m.headers = append(m.headers, h)
		m.bufs = append(m.bufs, (*int16)(buf))
		if r, _, _ := procWaveInPrepare.Call(m.handle, uintptr(unsafe.Pointer(h)), hdrSize); r != 0 {
			m.release()
			return nil, fmt.Errorf("can't prepare the microphone: %s", mmError(r))
		}
		if r, _, _ := procWaveInAddBuffer.Call(m.handle, uintptr(unsafe.Pointer(h)), hdrSize); r != 0 {
			m.release()
			return nil, fmt.Errorf("can't prepare the microphone: %s", mmError(r))
		}
	}
	if r, _, _ := procWaveInStart.Call(m.handle); r != 0 {
		m.release()
		return nil, fmt.Errorf("can't start the microphone: %s", mmError(r))
	}
	go m.run()
	return m, nil
}

func (m *mic) run() {
	defer close(m.done)
	defer close(m.frames)
	next := 0
	for {
		select {
		case <-m.stop:
			return
		default:
		}
		windows.WaitForSingleObject(m.event, 200)
		// Buffers complete in the order they were queued.
		for {
			h := m.headers[next]
			if h.Flags&whdrDone == 0 {
				break
			}
			n := int(h.BytesRecorded / 2)
			samples := make([]int16, n)
			copy(samples, unsafe.Slice(m.bufs[next], n))
			h.Flags &^= whdrDone
			h.BytesRecorded = 0
			if r, _, _ := procWaveInAddBuffer.Call(m.handle, uintptr(unsafe.Pointer(h)), hdrSize); r != 0 {
				m.setErr(fmt.Errorf("microphone stopped: %s", mmError(r)))
				return
			}
			next = (next + 1) % numBuffers
			if n == 0 {
				continue
			}
			select {
			case m.frames <- samples:
			case <-m.stop:
				return
			}
		}
	}
}

func (m *mic) setErr(err error) {
	m.mu.Lock()
	m.err = err
	m.mu.Unlock()
}

func (m *mic) Frames() <-chan []int16 { return m.frames }

func (m *mic) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

func (m *mic) Close() error {
	m.once.Do(func() {
		close(m.stop)
		<-m.done
		m.release()
	})
	return nil
}

// release stops the device and frees the buffers. waveInReset hands every queued buffer
// back before they are unprepared and the memory is freed.
func (m *mic) release() {
	procWaveInReset.Call(m.handle)
	for _, h := range m.headers {
		procWaveInUnprepare.Call(m.handle, uintptr(unsafe.Pointer(h)), hdrSize)
	}
	procWaveInClose.Call(m.handle)
	if m.mem != 0 {
		windows.LocalFree(m.mem)
		m.mem = 0
	}
	windows.CloseHandle(m.event)
}

func mmError(code uintptr) string {
	var buf [256]uint16
	if r, _, _ := procWaveInGetErrText.Call(code, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))); r == 0 {
		return windows.UTF16ToString(buf[:])
	}
	return fmt.Sprintf("error %d", code)
}
