package protocol

import (
	"errors"
	"time"

	hid "github.com/sstallion/go-hid"
)

// reportReader is the part of *hid.Device that ReadReport needs. It is an interface only so
// the retry behaviour can be tested without hardware.
type reportReader interface {
	ReadWithTimeout(p []byte, timeout time.Duration) (int, error)
}

// maxTransientReadErrors is how many non-timeout read errors are retried before giving up.
const maxTransientReadErrors = 3

// ReadReport reads one input report, waiting up to timeout in total.
//
// hidapi's hidraw backend waits in poll(), which fails with EINTR ("Interrupted system call")
// whenever a signal lands on that thread. Go's runtime sends such signals routinely (goroutine
// preemption, garbage collection), so an otherwise healthy read can fail at random. That showed
// up as an aborted run within a handful of reads, and disappeared with async preemption off.
//
// A read that fails for any reason other than a timeout is therefore retried (up to
// maxTransientReadErrors times), resuming with the time that is left. Any error is retried
// rather than matching on the message, because hidapi builds it from strerror(), which is
// translated to the user's locale. A genuinely dead device (unplugged) keeps failing at once,
// so the retries cost nothing there.
func ReadReport(r reportReader, buf []byte, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	failures := 0
	for {
		wait := time.Until(deadline)
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		n, err := r.ReadWithTimeout(buf, wait)
		if err == nil || errors.Is(err, hid.ErrTimeout) {
			return n, err
		}
		failures++
		if failures > maxTransientReadErrors || !time.Now().Before(deadline) {
			return n, err
		}
	}
}
