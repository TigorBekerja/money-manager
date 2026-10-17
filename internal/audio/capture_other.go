//go:build !windows

package audio

import "errors"

// OpenMic is only implemented for Windows.
func OpenMic() (Source, error) {
	return nil, errors.New("microphone recording is only implemented for Windows")
}
