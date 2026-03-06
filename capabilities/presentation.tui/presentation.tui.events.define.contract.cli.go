package tui

// ErrMsg wraps errors so we can differentiate our application errors
// from benign background errors (like clipboard missing) coming from bubbles.
type ErrMsg struct {
	Err error
}

// Error implements the error interface.
func (e ErrMsg) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "unknown error"
}
