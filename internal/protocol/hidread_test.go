package protocol

import (
	"errors"
	"testing"
	"time"

	hid "github.com/sstallion/go-hid"
)

type scriptedRead struct {
	n   int
	err error
	// sleep simulates time passing inside the read before it returns
	sleep time.Duration
}

type scriptedReader struct {
	steps    []scriptedRead
	timeouts []time.Duration // the timeout each call was given
}

func (s *scriptedReader) ReadWithTimeout(p []byte, timeout time.Duration) (int, error) {
	s.timeouts = append(s.timeouts, timeout)
	if len(s.steps) == 0 {
		return 0, hid.ErrTimeout
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	time.Sleep(step.sleep)
	return step.n, step.err
}

var errInterrupted = errors.New("Interrupted system call")

func TestReadReportReturnsDataFirstTry(t *testing.T) {
	r := &scriptedReader{steps: []scriptedRead{{n: 64}}}
	n, err := ReadReport(r, make([]byte, 64), time.Second)
	if n != 64 || err != nil || len(r.timeouts) != 1 {
		t.Fatalf("n=%d err=%v calls=%d", n, err, len(r.timeouts))
	}
}

func TestReadReportRetriesInterruptedReads(t *testing.T) {
	r := &scriptedReader{steps: []scriptedRead{{err: errInterrupted}, {err: errInterrupted}, {n: 64}}}
	n, err := ReadReport(r, make([]byte, 64), time.Second)
	if n != 64 || err != nil {
		t.Fatalf("a read interrupted twice should still succeed, got n=%d err=%v", n, err)
	}
	if len(r.timeouts) != 3 {
		t.Errorf("want 3 attempts, got %d", len(r.timeouts))
	}
}

func TestReadReportDoesNotRetryATimeout(t *testing.T) {
	r := &scriptedReader{steps: []scriptedRead{{err: hid.ErrTimeout}, {n: 64}}}
	_, err := ReadReport(r, make([]byte, 64), time.Second)
	if !errors.Is(err, hid.ErrTimeout) || len(r.timeouts) != 1 {
		t.Fatalf("a timeout is an answer, not a failure: err=%v calls=%d", err, len(r.timeouts))
	}
}

func TestReadReportGivesUpOnAPersistentError(t *testing.T) {
	r := &scriptedReader{steps: []scriptedRead{{err: errInterrupted}, {err: errInterrupted}, {err: errInterrupted}, {err: errInterrupted}, {n: 64}}}
	_, err := ReadReport(r, make([]byte, 64), time.Second)
	if err == nil || len(r.timeouts) != maxTransientReadErrors+1 {
		t.Fatalf("want a bounded number of attempts (%d) then the error, got err=%v calls=%d", maxTransientReadErrors+1, err, len(r.timeouts))
	}
}

func TestReadReportResumesWithTheRemainingTime(t *testing.T) {
	r := &scriptedReader{steps: []scriptedRead{{err: errInterrupted, sleep: 60 * time.Millisecond}, {n: 64}}}
	if _, err := ReadReport(r, make([]byte, 64), 200*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if len(r.timeouts) != 2 || r.timeouts[1] > r.timeouts[0]-50*time.Millisecond {
		t.Errorf("second attempt should only get what is left of the budget, got %v", r.timeouts)
	}
}

func TestReadReportDoesNotRetryPastTheDeadline(t *testing.T) {
	r := &scriptedReader{steps: []scriptedRead{{err: errInterrupted, sleep: 40 * time.Millisecond}, {n: 64}}}
	_, err := ReadReport(r, make([]byte, 64), 20*time.Millisecond)
	if err == nil || len(r.timeouts) != 1 {
		t.Fatalf("no time left, so no retry: err=%v calls=%d", err, len(r.timeouts))
	}
}
