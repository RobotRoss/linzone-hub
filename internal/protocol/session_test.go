package protocol

import (
	"bytes"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	hid "github.com/sstallion/go-hid"
)

// chanRW plays a headset handle: reads block (up to their timeout) until a report is pushed with
// push, writes are recorded and passed to onWrite, which may push replies. Unlike fakeRW it
// blocks, as a real handle does, so a reader goroutine on it does not spin.
type chanRW struct {
	in   chan []byte
	fail chan struct{} // closed to make reads fail like an unplugged device

	mu      sync.Mutex
	writes  [][]byte
	onWrite func(report []byte)

	closes int32
}

func newChanRW() *chanRW {
	return &chanRW{in: make(chan []byte, 64), fail: make(chan struct{})}
}

func (c *chanRW) push(r []byte) { c.in <- r }

func (c *chanRW) ReadWithTimeout(p []byte, timeout time.Duration) (int, error) {
	select {
	case b := <-c.in:
		return copy(p, b), nil
	case <-c.fail:
		return 0, errors.New("device gone")
	case <-time.After(timeout):
		return 0, hid.ErrTimeout
	}
}

func (c *chanRW) Write(p []byte) (int, error) {
	cp := append([]byte(nil), p...)
	c.mu.Lock()
	c.writes = append(c.writes, cp)
	fn := c.onWrite
	c.mu.Unlock()
	if fn != nil {
		fn(cp)
	}
	return len(p), nil
}

func (c *chanRW) closeFn() error { atomic.AddInt32(&c.closes, 1); return nil }

func (c *chanRW) written() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]byte(nil), c.writes...)
}

// newTestSession starts a Session whose notifications arrive on the returned channel.
func newTestSession(t *testing.T, rw *chanRW) (*Session, chan *HciPacket) {
	t.Helper()
	notes := make(chan *HciPacket, 32)
	s := newSession(rw, rw.closeFn, func(p *HciPacket) { notes <- p })
	t.Cleanup(func() { _ = s.Close() })
	return s, notes
}

func waitNote(t *testing.T, notes chan *HciPacket) *HciPacket {
	t.Helper()
	select {
	case p := <-notes:
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("no notification arrived")
		return nil
	}
}

func TestSessionQueryReturnsTheReply(t *testing.T) {
	rw := newChanRW()
	rw.onWrite = func([]byte) { rw.push(padded(event(0x14, 4, TypeReply, 1, 0x00, 0x5E))) }
	s, _ := newTestSession(t, rw)

	pkt, err := s.Query(AddrRX, 4, time.Second)
	if err != nil || !bytes.Equal(pkt.Param, []byte{0x00, 0x5E}) {
		t.Fatalf("pkt=%+v err=%v", pkt, err)
	}
	if got := rw.written()[0][8]; got != 0x41 {
		t.Errorf("address byte = 0x%02X, want 0x41", got)
	}
}

func TestSessionDeliversNotificationsAndKeepsThemApartFromReplies(t *testing.T) {
	rw := newChanRW()
	// the headset sends a notification for the same event just before the reply
	rw.onWrite = func([]byte) {
		rw.push(padded(event(0x14, 65, TypeNotify, 1, 2, 10, 0xFF, 0)))
		rw.push(padded(event(0x14, 65, TypeReply, 1, 1, 10, 0xFF, 1)))
	}
	s, notes := newTestSession(t, rw)

	pkt, err := s.Query(AddrRX, 65, time.Second)
	if err != nil || !bytes.Equal(pkt.Param, []byte{1, 10, 0xFF, 1}) {
		t.Fatalf("the query must get the reply, not the notification: pkt=%+v err=%v", pkt, err)
	}
	n := waitNote(t, notes)
	if n.EventID != 65 || n.EventType != TypeNotify || !bytes.Equal(n.Param, []byte{2, 10, 0xFF, 0}) {
		t.Errorf("notification: %+v", n)
	}
	if s.Skipped() != 0 {
		t.Errorf("a notification is delivered, not set aside: skipped=%d", s.Skipped())
	}
}

func TestSessionDeliversNotificationsWithNoRequestInFlight(t *testing.T) {
	rw := newChanRW()
	s, notes := newTestSession(t, rw)
	_ = s
	rw.push(padded(event(0x14, 97, TypeNotify, 1, 1, 1))) // Bluetooth: radio on, device connected
	n := waitNote(t, notes)
	if n.EventID != 97 || !bytes.Equal(n.Param, []byte{1, 1}) {
		t.Errorf("notification: %+v", n)
	}
}

func TestSessionSetNumbersTxIDsAndMatchesTheAck(t *testing.T) {
	rw := newChanRW()
	rw.onWrite = func(report []byte) {
		sent, err := parseCommand(report)
		if err != nil {
			t.Errorf("bad command: %v", err)
			return
		}
		rw.push(padded(event(0x14, sent.EventID, TypeAck, sent.TxID, sent.Param...)))
	}
	s, _ := newTestSession(t, rw)

	for i, want := range []uint16{1, 2} {
		ack, err := s.Set(AddrRX, 35, []byte{byte(i + 1), 0xFF}, time.Second)
		if err != nil || ack.TxID != want || ack.Param[0] != byte(i+1) {
			t.Fatalf("set %d: ack=%+v err=%v", i, ack, err)
		}
	}
	if ack, err := s.Set(AddrRX, 34, []byte{0x32}, time.Second); err != nil || ack.TxID != 1 {
		t.Errorf("TxIDs count per event: ack=%+v err=%v", ack, err)
	}
}

func TestSessionSetsAsideTrafficThatIsNotTheAnswer(t *testing.T) {
	rw := newChanRW()
	rw.onWrite = func([]byte) {
		rw.push([]byte{0x80, 0x01, 0x00})                          // media key: not HCI
		rw.push(padded(event(0x14, 33, TypeReply, 1, 0, 5, 0xFF))) // reply to another query
		rw.push(padded(event(0x14, 4, TypeReply, 9, 0, 0x5E)))     // right event, wrong TxID
		rw.push(padded(event(0x12, 4, TypeReply, 1, 0, 0x5E)))     // right event, from the transceiver
		rw.push(padded(event(0x14, 4, TypeReply, 1, 0, 0x5C)))     // the answer
	}
	s, _ := newTestSession(t, rw)

	pkt, err := s.Query(AddrRX, 4, time.Second)
	if err != nil || pkt.Param[1] != 0x5C {
		t.Fatalf("pkt=%+v err=%v", pkt, err)
	}
	if s.Skipped() != 4 {
		t.Errorf("want 4 reports set aside, got %d", s.Skipped())
	}
}

func TestSessionQueryTimesOutAndCarriesOn(t *testing.T) {
	rw := newChanRW()
	silent := true
	rw.onWrite = func([]byte) {
		if !silent {
			rw.push(padded(event(0x14, 4, TypeReply, 1, 0, 0x5E)))
		}
	}
	s, _ := newTestSession(t, rw)

	if _, err := s.Query(AddrRX, 37, 50*time.Millisecond); !errors.Is(err, ErrNoReply) {
		t.Fatalf("want ErrNoReply, got %v", err)
	}
	silent = false
	if pkt, err := s.Query(AddrRX, 4, time.Second); err != nil || pkt.Param[1] != 0x5E {
		t.Fatalf("a timeout must not wedge the session: pkt=%+v err=%v", pkt, err)
	}
}

func TestSessionLateReplyAfterTimeoutIsNotMistakenForTheNextAnswer(t *testing.T) {
	rw := newChanRW()
	s, notes := newTestSession(t, rw)
	_ = notes

	if _, err := s.Query(AddrRX, 4, 30*time.Millisecond); !errors.Is(err, ErrNoReply) {
		t.Fatalf("want ErrNoReply, got %v", err)
	}
	rw.push(padded(event(0x14, 4, TypeReply, 1, 0, 0x11))) // the late reply to the first query
	time.Sleep(50 * time.Millisecond)
	rw.onWrite = func([]byte) { rw.push(padded(event(0x14, 33, TypeReply, 1, 0, 7, 0xFF))) }
	pkt, err := s.Query(AddrRX, 33, time.Second)
	if err != nil || pkt.EventID != 33 {
		t.Fatalf("pkt=%+v err=%v", pkt, err)
	}
}

func TestSessionReaderFailureEndsAPendingRequest(t *testing.T) {
	rw := newChanRW()
	s, _ := newTestSession(t, rw)

	errc := make(chan error, 1)
	go func() { _, err := s.Query(AddrRX, 4, 5*time.Second); errc <- err }()
	time.Sleep(50 * time.Millisecond)
	close(rw.fail) // unplugged

	select {
	case err := <-errc:
		if !errors.Is(err, ErrSessionClosed) {
			t.Fatalf("want ErrSessionClosed, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the pending request was not woken when the device failed")
	}
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("Done not closed")
	}
	if s.Err() == nil || s.Stopped() {
		t.Errorf("a device failure is an error, not a Close: err=%v stopped=%v", s.Err(), s.Stopped())
	}
	if _, err := s.Query(AddrRX, 4, time.Second); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("a dead session must refuse new requests, got %v", err)
	}
}

func TestSessionCloseWakesARequestAndClosesTheHandleOnce(t *testing.T) {
	rw := newChanRW()
	s, _ := newTestSession(t, rw)

	errc := make(chan error, 1)
	go func() { _, err := s.Query(AddrRX, 4, 5*time.Second); errc <- err }()
	time.Sleep(50 * time.Millisecond)

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if !errors.Is(err, ErrSessionClosed) {
			t.Fatalf("want ErrSessionClosed, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not wake the request")
	}
	_ = s.Close() // idempotent
	if n := atomic.LoadInt32(&rw.closes); n != 1 {
		t.Errorf("handle closed %d times, want 1", n)
	}
	if !s.Stopped() || s.Err() != nil {
		t.Errorf("stopped=%v err=%v", s.Stopped(), s.Err())
	}
}

func TestSessionSerialisesConcurrentRequests(t *testing.T) {
	rw := newChanRW()
	rw.onWrite = func(report []byte) {
		// answer whatever event the GET asked for, echoing it in the param
		ev := report[2+7]
		rw.push(padded(event(0x14, ev, TypeReply, 1, ev)))
	}
	s, _ := newTestSession(t, rw)

	var wg sync.WaitGroup
	for _, ev := range []byte{2, 3, 4, 33, 34, 35, 36, 65} {
		wg.Add(1)
		go func(ev byte) {
			defer wg.Done()
			pkt, err := s.Query(AddrRX, ev, 2*time.Second)
			if err != nil || pkt.EventID != ev || pkt.Param[0] != ev {
				t.Errorf("event %d: pkt=%+v err=%v", ev, pkt, err)
			}
		}(ev)
	}
	wg.Wait()
}
