package main

import (
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/patyhank/linzone-hub/internal/protocol"
)

// fakeLink simulates a headset: it answers listed events after a delay, never answers the
// others, can drop replies, and can inject unrelated notifications.
type fakeLink struct {
	mu      sync.Mutex
	answers map[byte]time.Duration // event -> reply delay; absent = never answers
	dropOn  map[byte]int           // event -> drop this many of its replies before answering
	ch      chan *protocol.HciPacket
}

func newFake(answers map[byte]time.Duration) *fakeLink {
	return &fakeLink{answers: answers, dropOn: map[byte]int{}, ch: make(chan *protocol.HciPacket, 64)}
}

func (f *fakeLink) SendGet(id byte) error {
	f.mu.Lock()
	delay, ok := f.answers[id]
	if ok && f.dropOn[id] > 0 {
		f.dropOn[id]--
		ok = false
	}
	f.mu.Unlock()
	if ok {
		time.AfterFunc(delay, func() { f.ch <- &protocol.HciPacket{EventID: id, EventType: 0x10, Param: []byte{id, 7}} })
	}
	return nil
}

func (f *fakeLink) notify(id byte) {
	f.ch <- &protocol.HciPacket{EventID: id, EventType: 0xA0, Param: []byte{0xEE}}
}

func (f *fakeLink) Recv(timeout time.Duration) (*protocol.HciPacket, error) {
	select {
	case p := <-f.ch:
		return p, nil
	case <-time.After(timeout):
		return nil, errLinkTimeout
	}
}

var testEvents = []burstEvent{{"a", 2}, {"b", 3}, {"c", 4}, {"silent", 37}}

func TestSequentialRecordsAnsweredAndSilent(t *testing.T) {
	f := newFake(map[byte]time.Duration{2: time.Millisecond, 3: time.Millisecond, 4: time.Millisecond})
	obs, _, total, err := runSequential(f, testEvents, 40*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 3 || obs[37].param != nil {
		t.Fatalf("want 3 answered and the silent one absent, got %v", obs)
	}
	if total < 40*time.Millisecond {
		t.Errorf("the silent query should cost its full timeout, total %v", total)
	}
}

func TestBurstMatchesOutOfOrderRepliesByEventID(t *testing.T) {
	// replies arrive in reverse order of the queries
	f := newFake(map[byte]time.Duration{2: 30 * time.Millisecond, 3: 20 * time.Millisecond, 4: 10 * time.Millisecond})
	res, err := runBurst(f, testEvents, map[byte]bool{2: true, 3: true, 4: true}, 0, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.missing) != 0 || res.completed == 0 {
		t.Fatalf("expected all replies, missing %v", res.missing)
	}
	for _, id := range []byte{2, 3, 4} {
		if got := res.obs[id].param; len(got) != 2 || got[0] != id {
			t.Errorf("event %d matched the wrong packet: %v", id, got)
		}
	}
	if res.completed >= 200*time.Millisecond {
		t.Errorf("burst should finish when the last reply lands (~30 ms), not wait for the silent query; took %v", res.completed)
	}
}

func TestBurstCountsUnrelatedPacketsAndKeepsMatching(t *testing.T) {
	f := newFake(map[byte]time.Duration{2: 5 * time.Millisecond, 3: 5 * time.Millisecond, 4: 5 * time.Millisecond})
	f.notify(200) // an event we never asked about, already queued
	res, err := runBurst(f, testEvents, map[byte]bool{2: true, 3: true, 4: true}, 0, 500*time.Millisecond)
	if err != nil || len(res.missing) != 0 {
		t.Fatalf("err=%v missing=%v", err, res.missing)
	}
	if res.other != 1 {
		t.Errorf("want the stray notification counted as unrelated, got %d", res.other)
	}
}

// A notification (0xA0) for an event we also asked about must not be mistaken for its reply.
func TestRepliesAreMatchedOnTypeNotJustEventID(t *testing.T) {
	f := newFake(map[byte]time.Duration{2: 5 * time.Millisecond, 3: 20 * time.Millisecond, 4: 5 * time.Millisecond})
	f.notify(3) // arrives first, same event ID as a query we are waiting on, type 0xA0
	res, err := runBurst(f, testEvents, map[byte]bool{2: true, 3: true, 4: true}, 0, 500*time.Millisecond)
	if err != nil || len(res.missing) != 0 {
		t.Fatalf("err=%v missing=%v", err, res.missing)
	}
	if got := res.obs[3]; got.typ != 0x10 || got.param[0] != 3 {
		t.Errorf("event 3 took the notification instead of the reply: %+v", got)
	}
	if res.other != 1 {
		t.Errorf("the notification should be counted as unrelated, got %d", res.other)
	}

	// sequential: same trap
	f2 := newFake(map[byte]time.Duration{2: 5 * time.Millisecond, 3: 20 * time.Millisecond, 4: 5 * time.Millisecond})
	f2.notify(2)
	obs, other, _, err := runSequential(f2, testEvents, 80*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if obs[2].typ != 0x10 || other != 1 {
		t.Errorf("sequential matched a notification: %+v other=%d", obs[2], other)
	}
}

func TestBurstReportsALostReplyAfterTheDeadline(t *testing.T) {
	f := newFake(map[byte]time.Duration{2: time.Millisecond, 3: time.Millisecond, 4: time.Millisecond})
	f.dropOn[3] = 1 // the headset swallows event 3 once
	res, err := runBurst(f, testEvents, map[byte]bool{2: true, 3: true, 4: true}, 0, 60*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.missing) != 1 || res.missing[0] != 3 || res.completed != 0 {
		t.Fatalf("want exactly event 3 reported lost, got missing=%v completed=%v", res.missing, res.completed)
	}
}

func TestBurstGapPacesTheWrites(t *testing.T) {
	f := newFake(map[byte]time.Duration{2: time.Millisecond, 3: time.Millisecond, 4: time.Millisecond})
	res, err := runBurst(f, testEvents, map[byte]bool{2: true, 3: true, 4: true}, 10*time.Millisecond, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if res.sentIn < 30*time.Millisecond { // 4 writes -> 3 gaps of 10 ms
		t.Errorf("gap not honoured, writes took %v", res.sentIn)
	}
}

func TestMedianDuration(t *testing.T) {
	d := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	if got := medianDuration([]time.Duration{d(3), d(1), d(2)}); got != d(2) {
		t.Errorf("odd median = %v", got)
	}
	if got := medianDuration([]time.Duration{d(4), d(1), d(2), d(3)}); got != 2500*time.Microsecond {
		t.Errorf("even median = %v", got)
	}
	if medianDuration(nil) != 0 {
		t.Error("empty median should be 0")
	}
}

// Real packets captured from an H9 II (volume wheel and ANC button), padded to 64 bytes the way the headset sends them.
func TestHeadsetPayloadParsesCapturedReports(t *testing.T) {
	pad := strings.Repeat("a5", 64)
	cases := []struct {
		hex   string
		event byte
		typ   byte
	}{
		{"020f" + "04ff0c0096c31421a00100001dff4b", 33, 0xA0},   // headphone volume notification
		{"0210" + "04ff0d0096c31441a00100010aff015a", 65, 0xA0}, // ambient mode notification
	}
	for _, c := range cases {
		raw, err := hex.DecodeString((c.hex + pad)[:128])
		if err != nil {
			t.Fatal(err)
		}
		payload, err := headsetPayload(raw)
		if err != nil {
			t.Fatal(err)
		}
		pkt, err := protocol.ParseHciEvent(payload)
		if err != nil || pkt.EventID != c.event || pkt.EventType != c.typ {
			t.Errorf("event %d: pkt=%+v err=%v", c.event, pkt, err)
		}
	}
	for _, notHCI := range [][]byte{{0x0c, 0x04}, {0x05, 0x01}, {0x02}, nil} {
		if _, err := headsetPayload(notHCI); err == nil {
			t.Errorf("%v should not be treated as an HCI report", notHCI)
		}
	}
}
