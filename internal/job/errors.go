package job

import "errors"

// ErrNoAvailableTasks is the sentinel behind RunNext's "no available leaf"
// failures. next and claim --next want it to fail loudly with the existing
// message text verbatim (a caller explicitly asked for a task and got
// none); orient wants to catch the same condition and treat it as a valid,
// exit-0 answer instead. Wrapping the formatted message in
// errNoAvailableTasks keeps both: errors.Is(err, ErrNoAvailableTasks) finds
// the sentinel through Unwrap, while err.Error() stays exactly the
// caller-facing text next/claim already print.
var ErrNoAvailableTasks = errors.New("no available tasks")

// errNoAvailableTasks pairs a fully-formatted, caller-facing message with
// the ErrNoAvailableTasks sentinel.
type errNoAvailableTasks struct {
	msg string
}

func newErrNoAvailableTasks(msg string) error {
	return &errNoAvailableTasks{msg: msg}
}

func (e *errNoAvailableTasks) Error() string { return e.msg }
func (e *errNoAvailableTasks) Unwrap() error { return ErrNoAvailableTasks }
