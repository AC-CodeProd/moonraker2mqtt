package network

import "context"

// DiagnosticReason classifies fixed session outcomes without inspecting or
// printing arbitrary wrapped transport errors. No policy or timing changes.
func DiagnosticReason(err error) uint8 {
	switch err {
	case nil:
		return 0
	case context.Canceled:
		return 2
	case context.DeadlineExceeded:
		return 3
	case ErrDown:
		return 4
	case ErrShutdown:
		return 5
	default:
		return 1
	}
}
