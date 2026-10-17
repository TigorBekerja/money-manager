package audio

// Access is the result of the operating system's microphone privacy check.
type Access int

const (
	Unknown Access = iota
	Allowed
	Denied
)
