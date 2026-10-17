//go:build windows

package audio

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	winmm               = windows.NewLazySystemDLL("winmm.dll")
	procWaveInGetNumDev = winmm.NewProc("waveInGetNumDevs")
	procWaveInGetCaps   = winmm.NewProc("waveInGetDevCapsW")
)

// waveInCaps mirrors WAVEINCAPSW.
type waveInCaps struct {
	Mid           uint16
	Pid           uint16
	DriverVersion uint32
	Pname         [32]uint16
	Formats       uint32
	Channels      uint16
	Reserved1     uint16
}

// InputDevices lists the names of the microphones (audio input devices) Windows reports.
func InputDevices() ([]string, error) {
	if err := procWaveInGetNumDev.Find(); err != nil {
		return nil, fmt.Errorf("winmm.dll not available: %w", err)
	}
	n, _, _ := procWaveInGetNumDev.Call()
	var names []string
	for i := uintptr(0); i < n; i++ {
		var caps waveInCaps
		r, _, _ := procWaveInGetCaps.Call(i, uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps))
		if r != 0 { // MMSYSERR_NOERROR
			names = append(names, fmt.Sprintf("input device %d", i+1))
			continue
		}
		// Windows truncates names to 31 characters.
		names = append(names, strings.TrimSpace(windows.UTF16ToString(caps.Pname[:])))
	}
	return names, nil
}

// MicrophoneAccess reports whether Windows privacy settings let desktop apps use the
// microphone. detail names the setting that blocks access, if any.
func MicrophoneAccess() (access Access, detail string) {
	const base = `Software\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\microphone`
	settings := []struct {
		root registry.Key
		path string
		name string
	}{
		{registry.LOCAL_MACHINE, base, `"Microphone access" for this device`},
		{registry.CURRENT_USER, base, `"Microphone access"`},
		{registry.CURRENT_USER, base + `\NonPackaged`, `"Let desktop apps access your microphone"`},
	}
	known := false
	for _, s := range settings {
		k, err := registry.OpenKey(s.root, s.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, _, err := k.GetStringValue("Value")
		k.Close()
		if err != nil {
			continue
		}
		known = true
		if v == "Deny" {
			return Denied, s.name + " is turned off"
		}
	}
	if !known {
		return Unknown, "no privacy setting found (Windows allows access by default)"
	}
	return Allowed, "allowed for desktop apps"
}
