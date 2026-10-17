package audio

import (
	"math"
	"sort"
	"time"
)

// SampleRate is the format used everywhere: 16 kHz mono 16-bit, what Whisper expects.
const SampleRate = 16000

// SegmenterConfig tunes speech detection. Durations are converted to 20 ms frames.
type SegmenterConfig struct {
	Calibration time.Duration // initial quiet period used to measure background noise
	Silence     time.Duration // pause that ends an utterance
	PreRoll     time.Duration // audio kept from before speech was detected
	MinSpeech   time.Duration // shorter bursts (clicks, bumps) are dropped
	MaxLength   time.Duration // an utterance is cut here
	MinLevel    float64       // lowest RMS treated as speech, protects against a silent calibration
}

// DefaultSegmenterConfig is tuned for entries with a note. The pause that ends an entry
// is long enough for a slow speaker to stop and think before the note ("... ribu, eh,
// makan siang") without the entry ending there (was 0.8 s, see DLR-023).
func DefaultSegmenterConfig() SegmenterConfig {
	return SegmenterConfig{
		Calibration: 500 * time.Millisecond,
		Silence:     2500 * time.Millisecond,
		PreRoll:     300 * time.Millisecond,
		MinSpeech:   250 * time.Millisecond,
		MaxLength:   30 * time.Second,
		MinLevel:    300,
	}
}

type EventKind int

const (
	Calibrated    EventKind = iota // background noise measured, listening for speech
	Level                          // meter update, Level in 0..1
	SpeechStarted                  // At = when speech began
	Utterance                      // Samples = the speech, At = when it began
	Dropped                        // speech too short to be an entry
)

type Event struct {
	Kind    EventKind
	Level   float64
	At      time.Time
	Samples []int16
}

// Segmenter splits a continuous 16 kHz stream into utterances using signal energy.
type Segmenter struct {
	cfg        SegmenterConfig
	start      time.Time // wall-clock time of the first sample
	pos        int64     // samples consumed
	frame      []int16   // partial 20 ms frame
	calibFrame int
	calibRMS   []float64
	noise      float64
	calibrated bool

	speaking   bool
	loudRun    int
	quietRun   int
	speechRun  int // frames of speech in the current utterance
	speechAt   int64
	pre        []int16 // ring of recent audio while idle
	utter      []int16
	levelEvery int
	frames     int
}

const frameSamples = SampleRate / 50 // 20 ms

func frames(d time.Duration) int { return int(d / (20 * time.Millisecond)) }

// NewSegmenter starts a segmenter for a stream whose first sample was captured at start.
func NewSegmenter(cfg SegmenterConfig, start time.Time) *Segmenter {
	return &Segmenter{cfg: cfg, start: start, calibFrame: max(1, frames(cfg.Calibration)), levelEvery: 5}
}

// Push feeds samples and returns the events they caused.
func (s *Segmenter) Push(samples []int16) []Event {
	var events []Event
	for len(samples) > 0 {
		n := min(frameSamples-len(s.frame), len(samples))
		s.frame = append(s.frame, samples[:n]...)
		samples = samples[n:]
		if len(s.frame) == frameSamples {
			events = s.processFrame(s.frame, events)
			s.frame = s.frame[:0]
		}
	}
	return events
}

// Flush ends a running utterance (when the user stops listening mid-sentence).
func (s *Segmenter) Flush() []Event {
	if !s.speaking {
		return nil
	}
	return s.endUtterance(nil)
}

func (s *Segmenter) timeAt(sample int64) time.Time {
	return s.start.Add(time.Duration(sample) * time.Second / SampleRate)
}

func rms(frame []int16) float64 {
	var sum float64
	for _, v := range frame {
		f := float64(v)
		sum += f * f
	}
	return math.Sqrt(sum / float64(len(frame)))
}

// meter maps RMS to 0..1 on a -60..-10 dBFS scale.
func meter(r float64) float64 {
	if r <= 0 {
		return 0
	}
	db := 20 * math.Log10(r/32768)
	return math.Max(0, math.Min(1, (db+60)/50))
}

// threshold is the RMS treated as speech: well above the background noise.
func (s *Segmenter) threshold() float64 { return math.Max(s.noise*2.5, s.cfg.MinLevel) }

// NoiseLevel is the measured background noise (0..1 meter scale), after calibration.
func (s *Segmenter) NoiseLevel() float64 { return meter(s.noise) }

func (s *Segmenter) processFrame(frame []int16, events []Event) []Event {
	r := rms(frame)
	framePos := s.pos
	s.pos += int64(len(frame))
	s.frames++
	if s.frames%s.levelEvery == 0 {
		events = append(events, Event{Kind: Level, Level: meter(r)})
	}

	if !s.calibrated {
		s.calibRMS = append(s.calibRMS, r)
		if len(s.calibRMS) >= s.calibFrame {
			// The median ignores a pop or click while the microphone starts.
			sort.Float64s(s.calibRMS)
			s.noise = s.calibRMS[len(s.calibRMS)/2]
			s.calibrated = true
			events = append(events, Event{Kind: Calibrated})
		}
		s.keepPreRoll(frame)
		return events
	}

	loud := r >= s.threshold()
	if !s.speaking {
		if loud {
			s.loudRun++
		} else {
			s.loudRun = 0
			s.noise = 0.95*s.noise + 0.05*r // follow slow changes in background noise
		}
		s.keepPreRoll(frame)
		if s.loudRun >= 3 { // 60 ms of speech
			s.speaking = true
			s.speechRun = s.loudRun
			s.quietRun = 0
			s.speechAt = framePos - int64((s.loudRun-1)*frameSamples)
			s.utter = append(s.utter[:0], s.pre...)
			events = append(events, Event{Kind: SpeechStarted, At: s.timeAt(s.speechAt)})
		}
		return events
	}

	s.utter = append(s.utter, frame...)
	if r >= s.threshold()*0.7 {
		s.quietRun = 0
		s.speechRun++
	} else {
		s.quietRun++
	}
	if s.quietRun >= frames(s.cfg.Silence) || len(s.utter) >= int(s.cfg.MaxLength/time.Second)*SampleRate {
		events = s.endUtterance(events)
	}
	return events
}

func (s *Segmenter) endUtterance(events []Event) []Event {
	s.speaking, s.loudRun = false, 0
	// Trim trailing silence to ~200 ms.
	keep := len(s.utter) - max(0, s.quietRun-10)*frameSamples
	samples := append([]int16(nil), s.utter[:max(0, keep)]...)
	s.utter, s.pre = s.utter[:0], s.pre[:0]
	at := s.timeAt(s.speechAt)
	if s.speechRun < frames(s.cfg.MinSpeech) {
		return append(events, Event{Kind: Dropped, At: at})
	}
	return append(events, Event{Kind: Utterance, At: at, Samples: samples})
}

func (s *Segmenter) keepPreRoll(frame []int16) {
	s.pre = append(s.pre, frame...)
	limit := int(s.cfg.PreRoll/time.Millisecond) * SampleRate / 1000
	if len(s.pre) > limit {
		s.pre = append(s.pre[:0], s.pre[len(s.pre)-limit:]...)
	}
}
