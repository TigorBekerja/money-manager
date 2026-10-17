//go:build !windows

package audio

import "errors"

func InputDevices() ([]string, error) {
	return nil, errors.New("microphone detection is only implemented for Windows")
}

func MicrophoneAccess() (Access, string) {
	return Unknown, "only checked on Windows"
}
