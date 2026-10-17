package audio

import (
	"encoding/binary"
	"testing"
)

func TestEncodeWAV(t *testing.T) {
	wav := EncodeWAV([]int16{0, 1, -1, 32767}, 16000)
	if len(wav) != 44+8 {
		t.Fatalf("len = %d, want 52", len(wav))
	}
	if string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" || string(wav[36:40]) != "data" {
		t.Errorf("bad chunk ids: %q", wav[:40])
	}
	le := binary.LittleEndian
	if got := le.Uint32(wav[4:]); got != 44 {
		t.Errorf("RIFF size = %d, want 44", got)
	}
	if got := le.Uint32(wav[24:]); got != 16000 {
		t.Errorf("sample rate = %d", got)
	}
	if got := le.Uint32(wav[28:]); got != 32000 {
		t.Errorf("byte rate = %d", got)
	}
	if got := le.Uint32(wav[40:]); got != 8 {
		t.Errorf("data size = %d", got)
	}
	if got := int16(le.Uint16(wav[48:])); got != -1 {
		t.Errorf("third sample = %d, want -1", got)
	}
}
