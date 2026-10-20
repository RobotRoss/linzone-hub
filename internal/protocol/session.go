package protocol

import (
	"errors"
	"fmt"
	"sync"
	"time"

	hid "github.com/sstallion/go-hid"
)

// ErrSessionClosed is returned (wrapped) by Session.Query and Session.Set once the session has
// been closed or its reader has stopped because the device went away.
var ErrSessionClosed = errors.New("headset session closed")

// sessionPoll is how long one read waits before the reader checks whether it was asked to stop.
// It bounds how long Close can take.
const sessionPoll = 200 * time.Millisecond

// Session owns one open headset HID handle for its whole life and keeps a reader goroutine
// running on it. That is what makes unsolicited notifications (type 0xA0: button presses, a
// device connecting over Bluetooth, battery drift...) visible: with a handle that is only read
// during a request they are lost.
//
// All traffic on the handle goes through the Session. The reader parses every report and
//
//   - hands a reply or acknowledgement to the request waiting for it (matched exactly as Link
//     does: event ID, type, TxID and source address),
//   - passes every notification to the notify callback,
//   - and sets anything else aside (replies to nobody, corrupt packets, media-key reports).
//
// Query and Set have the same signatures as on Link, so code written against either works with
// both. One request is in flight at a time; concurrent callers queue up.
type Session struct {
	rw      reportIO
	closeFn func() error

	reqMu sync.Mutex      // held for a whole request: write, wait for the answer
	tx    map[byte]uint16 // last TxID used per event; guarded by reqMu

	mu      sync.Mutex
	wait    *sessionWait // the request currently waiting, if any
	skipped int
	err     error // why the reader stopped on its own, if it did
	notify  func(*HciPacket)

	stop      chan struct{} // closed by Close
	done      chan struct{} // closed when the reader goroutine has exited
	stopOnce  sync.Once
	closeOnce sync.Once
	closeErr  error
}

type sessionWait struct {
	event byte
	from  byte // address byte the answer must carry
	match func(*HciPacket) bool
	ch    chan *HciPacket // capacity 1
}

// NewSession starts a Session on an open headset HID handle and takes ownership of it: Close
// closes the handle. notify is called for every notification; it runs on the reader goroutine,
// so it must return quickly and must not call Query or Set.
func NewSession(dev *hid.Device, notify func(*HciPacket)) *Session {
	return newSession(dev, dev.Close, notify)
}

func newSession(rw reportIO, closeFn func() error, notify func(*HciPacket)) *Session {
	s := &Session{
		rw:      rw,
		closeFn: closeFn,
		notify:  notify,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go s.readLoop()
	return s
}

// Done is closed when the reader has stopped, either because Close was called or because the
// device failed (unplugged, powered off at the USB level). Err and Stopped tell which.
func (s *Session) Done() <-chan struct{} { return s.done }

// Stopped reports whether Close was called, as opposed to the device failing.
func (s *Session) Stopped() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}

// Err is why the reader stopped on its own, or nil.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Skipped is how many received reports were set aside as neither an answer nor a notification.
func (s *Session) Skipped() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skipped
}

// Close stops the reader, then closes the HID handle. It is safe to call more than once and
// from several goroutines. A request in flight returns an error wrapping ErrSessionClosed.
func (s *Session) Close() error {
	s.stopOnce.Do(func() { close(s.stop) })
	<-s.done // never close the handle under a read that is still running
	s.closeOnce.Do(func() {
		if s.closeFn != nil {
			s.closeErr = s.closeFn()
		}
	})
	return s.closeErr
}

func (s *Session) readLoop() {
	defer close(s.done)
	buf := make([]byte, linkReportSize)
	for {
		select {
		case <-s.stop:
			return
		default:
		}
		n, err := ReadReport(s.rw, buf, sessionPoll)
		if errors.Is(err, hid.ErrTimeout) {
			continue
		}
		if err != nil {
			s.mu.Lock()
			s.err = fmt.Errorf("read: %w", err)
			s.mu.Unlock()
			return
		}
		pkt, perr := ParseHeadsetReport(buf[:n])
		if perr != nil {
			s.mu.Lock()
			s.skipped++
			s.mu.Unlock()
			continue
		}
		s.dispatch(pkt)
	}
}

func (s *Session) dispatch(pkt *HciPacket) {
	s.mu.Lock()
	if w := s.wait; w != nil && pkt.EventID == w.event && pkt.Address == w.from && w.match(pkt) {
		s.wait = nil
		s.mu.Unlock()
		w.ch <- pkt
		return
	}
	isNotify := pkt.EventType == TypeNotify
	if !isNotify {
		s.skipped++
	}
	notify := s.notify
	s.mu.Unlock()
	if isNotify && notify != nil {
		notify(pkt)
	}
}

func (s *Session) clearWait(w *sessionWait) {
	s.mu.Lock()
	if s.wait == w {
		s.wait = nil
	}
	s.mu.Unlock()
}

// deadErr is the error for a request that cannot be served because the reader is gone.
func (s *Session) deadErr() error {
	if err := s.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrSessionClosed, err)
	}
	return ErrSessionClosed
}

// Query sends a GET for event to dst (AddrRX = the headset, AddrTX = the USB transceiver) and
// returns its reply. It returns an error wrapping ErrNoReply if none arrives within timeout.
func (s *Session) Query(dst byte, event byte, timeout time.Duration) (*HciPacket, error) {
	s.reqMu.Lock()
	defer s.reqMu.Unlock()
	payload := BuildHciGetWithAddress(event, byte(dst<<4|AddrPC), linkQueryTxID, nil)
	return s.exchange(dst, event, payload, timeout, "reply", func(p *HciPacket) bool {
		return p.EventType == TypeReply && p.TxID == linkQueryTxID
	})
}

// Set sends a SET for event to dst and waits for the acknowledgement, which echoes the TxID.
// TxIDs count up per event starting at 1, as in the Windows app. It returns an error wrapping
// ErrNoReply if no acknowledgement arrives within timeout, in which case the change may or may
// not have been applied.
func (s *Session) Set(dst byte, event byte, param []byte, timeout time.Duration) (*HciPacket, error) {
	s.reqMu.Lock()
	defer s.reqMu.Unlock()
	if s.tx == nil {
		s.tx = map[byte]uint16{}
	}
	txid := s.tx[event] + 1
	if txid == 0 {
		txid = 1
	}
	s.tx[event] = txid
	payload := BuildHciSetWithAddress(event, byte(dst<<4|AddrPC), txid, param)
	return s.exchange(dst, event, payload, timeout, "acknowledgement", func(p *HciPacket) bool {
		return p.EventType == TypeAck && p.TxID == txid
	})
}

// exchange registers the waiter before writing, so an answer that arrives immediately cannot be
// missed, then waits for the reader to hand it over. The caller holds reqMu.
func (s *Session) exchange(dst byte, event byte, payload []byte, timeout time.Duration, what string, match func(*HciPacket) bool) (*HciPacket, error) {
	report, err := frameHeadsetReport(payload)
	if err != nil {
		return nil, err
	}
	select {
	case <-s.done:
		return nil, fmt.Errorf("event %d: %w", event, s.deadErr())
	default:
	}

	w := &sessionWait{event: event, from: byte(AddrPC<<4 | dst), match: match, ch: make(chan *HciPacket, 1)}
	s.mu.Lock()
	s.wait = w
	s.mu.Unlock()

	if _, err := s.rw.Write(report); err != nil {
		s.clearWait(w)
		return nil, fmt.Errorf("event %d: write: %w", event, err)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case pkt := <-w.ch:
		return pkt, nil
	case <-timer.C:
		s.clearWait(w)
		// the answer may have been handed over just as the timer fired
		select {
		case pkt := <-w.ch:
			return pkt, nil
		default:
		}
		return nil, fmt.Errorf("event %d: no %s: %w", event, what, ErrNoReply)
	case <-s.done:
		s.clearWait(w)
		select {
		case pkt := <-w.ch:
			return pkt, nil
		default:
		}
		return nil, fmt.Errorf("event %d: %w", event, s.deadErr())
	}
}
