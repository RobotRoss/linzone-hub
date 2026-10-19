package protocol

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	hid "github.com/sstallion/go-hid"
)

type fakeRead struct {
	data []byte
	err  error
}

// fakeRW records writes and replays scripted reads; once the script runs out, reads time out.
type fakeRW struct {
	writes   [][]byte
	reads    []fakeRead
	writeErr error
}

func (f *fakeRW) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	f.writes = append(f.writes, append([]byte(nil), p...))
	return len(p), nil
}

func (f *fakeRW) ReadWithTimeout(p []byte, timeout time.Duration) (int, error) {
	if len(f.reads) == 0 {
		return 0, hid.ErrTimeout
	}
	r := f.reads[0]
	f.reads = f.reads[1:]
	if r.err != nil {
		return 0, r.err
	}
	return copy(p, r.data), nil
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// padded wraps an HCI payload in the headset framing, padded to 64 bytes with the 0xA5 filler
// the headset leaves in its buffer.
func padded(payload []byte) []byte {
	r := bytes.Repeat([]byte{0xA5}, 64)
	r[0], r[1] = 0x02, byte(len(payload))
	copy(r[2:], payload)
	return r
}

// event builds a valid headset->host event packet.
func event(addr, id, typ byte, tx uint16, param ...byte) []byte {
	p := []byte{0x04, 0xFF, byte(9 + len(param)), 0x00, 0x96, 0xC3, addr, id, typ, byte(tx), byte(tx >> 8)}
	p = append(p, param...)
	sum := 0
	for _, b := range p[4:] {
		sum += int(b)
	}
	return append(p, byte(sum))
}

// Both packets below are quoted from the notes on the Windows app's traffic.
const (
	docGetEvent1ToTX = "02 0c 01 00 fc 08 96 c3 21 01 01 01 00 7d"
	docReplyEvent1   = "02 0d 04 ff 0a 00 96 c3 12 01 10 01 00 01 7e"
)

func TestQueryWritesTheWindowsAppsExactFrame(t *testing.T) {
	f := &fakeRW{reads: []fakeRead{{data: padded(mustHex(t, docReplyEvent1)[2:])}}}
	pkt, err := newLink(f).Query(AddrTX, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]byte, 64)
	copy(want, mustHex(t, docGetEvent1ToTX))
	if len(f.writes) != 1 || !bytes.Equal(f.writes[0], want) {
		t.Fatalf("frame differs from the app's:\n got  % X\n want % X", f.writes[0], want)
	}
	if pkt.EventID != 1 || len(pkt.Param) != 1 || pkt.Param[0] != 0x01 || pkt.Address != 0x12 || pkt.TxID != 1 {
		t.Errorf("unexpected reply: %+v", pkt)
	}
}

func TestQueryAddressesTheHeadsetAs0x41(t *testing.T) {
	f := &fakeRW{reads: []fakeRead{{data: padded(event(0x14, 4, TypeReply, 1, 0x00, 0x5E))}}}
	if _, err := newLink(f).Query(AddrRX, 4, time.Second); err != nil {
		t.Fatal(err)
	}
	if got := f.writes[0][8]; got != 0x41 {
		t.Errorf("address byte = 0x%02X, want 0x41", got)
	}
}

func TestParseRealCapturedNotifications(t *testing.T) {
	// volume wheel and ANC button notifications captured from an H9 II
	cases := []struct {
		payload string
		event   byte
		param   string
	}{
		{"04ff0c0096c31421a00100001dff4b", 33, "001dff"},
		{"04ff0d0096c31441a00100010aff015a", 65, "010aff01"},
	}
	for _, c := range cases {
		pkt, err := ParseHeadsetReport(padded(mustHex(t, c.payload)))
		if err != nil {
			t.Fatalf("event %d: %v", c.event, err)
		}
		if pkt.EventID != c.event || pkt.EventType != TypeNotify || pkt.TxID != 1 || hex.EncodeToString(pkt.Param) != c.param {
			t.Errorf("event %d: %+v", c.event, pkt)
		}
	}
}

func TestParseIgnoresWhateverFollowsTheLengthByte(t *testing.T) {
	payload := mustHex(t, "04ff0c0096c31421a00100001dff4b")
	garbage := padded(payload)
	for i := 2 + len(payload); i < len(garbage); i++ {
		garbage[i] = byte(i * 7) // not zero, not the usual filler either
	}
	pkt, err := ParseHeadsetReport(garbage)
	if err != nil || hex.EncodeToString(pkt.Param) != "001dff" {
		t.Fatalf("pkt=%+v err=%v", pkt, err)
	}
}

func TestQuerySetsAsideUnrelatedTrafficUntilTheRealReply(t *testing.T) {
	const ev = 65
	f := &fakeRW{reads: []fakeRead{
		{data: []byte{0x80, 0x01, 0x00}},                                                                                 // media-key report: not HCI
		{data: padded(event(0x14, ev, TypeNotify, 1, 1, 10, 0xFF, 1))},                                                   // notification, same event ID
		{data: padded(event(0x14, 33, TypeReply, 1, 0, 5, 0xFF))},                                                        // reply to some other query
		{data: padded(event(0x14, ev, TypeReply, 7, 1, 10, 0xFF, 1))},                                                    // right event, wrong TxID
		{data: padded(event(0x12, ev, TypeReply, 1, 1, 10, 0xFF, 1))},                                                    // right event, from the transceiver
		{data: func() []byte { b := padded(event(0x14, ev, TypeReply, 1, 1, 10, 0xFF, 1)); b[8+2] ^= 0xFF; return b }()}, // corrupted
		{data: padded(event(0x14, ev, TypeReply, 1, 2, 10, 0xFF, 0))},                                                    // the real reply
	}}
	l := newLink(f)
	pkt, err := l.Query(AddrRX, ev, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(pkt.Param) != "020aff00" {
		t.Errorf("took the wrong packet: % X", pkt.Param)
	}
	if l.Skipped() != 6 {
		t.Errorf("want 6 reports set aside, got %d", l.Skipped())
	}
}

func TestQueryReportsNoReply(t *testing.T) {
	_, err := newLink(&fakeRW{}).Query(AddrRX, 37, 50*time.Millisecond)
	if !errors.Is(err, ErrNoReply) || !strings.Contains(err.Error(), "37") {
		t.Fatalf("want an ErrNoReply naming the event, got %v", err)
	}
}

func TestQueryReportsNoReplyWhenOnlyUnrelatedTrafficArrives(t *testing.T) {
	f := &fakeRW{reads: []fakeRead{{data: padded(event(0x14, 33, TypeNotify, 1, 0, 9, 0xFF))}}}
	if _, err := newLink(f).Query(AddrRX, 65, 50*time.Millisecond); !errors.Is(err, ErrNoReply) {
		t.Fatalf("a notification must not satisfy a query, got %v", err)
	}
}

func TestQueryRetriesAnInterruptedRead(t *testing.T) {
	f := &fakeRW{reads: []fakeRead{{err: errors.New("Interrupted system call")}, {data: padded(event(0x14, 4, TypeReply, 1, 0, 0x5E))}}}
	pkt, err := newLink(f).Query(AddrRX, 4, time.Second)
	if err != nil || pkt.Param[1] != 0x5E {
		t.Fatalf("pkt=%+v err=%v", pkt, err)
	}
}

func TestQueryReturnsWriteErrors(t *testing.T) {
	boom := errors.New("device gone")
	_, err := newLink(&fakeRW{writeErr: boom}).Query(AddrRX, 4, time.Second)
	if !errors.Is(err, boom) || errors.Is(err, ErrNoReply) {
		t.Fatalf("a write failure is not a missing reply: %v", err)
	}
}

func TestParseRejectsMalformedEvents(t *testing.T) {
	good := mustHex(t, "04ff0c0096c31421a00100001dff4b")
	truncated := append([]byte(nil), good[:13]...) // length byte says more than we have
	badCksum := append([]byte(nil), good...)
	badCksum[len(badCksum)-1]++
	tiny := append([]byte(nil), good...)
	tiny[2] = 4
	notEvent := append([]byte(nil), good...)
	notEvent[0] = 0x01
	for name, payload := range map[string][]byte{"truncated": truncated, "bad checksum": badCksum, "PLEN too small": tiny, "not an event": notEvent} {
		if _, err := ParseHeadsetReport(padded(payload)); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
	for _, r := range [][]byte{nil, {0x02}, {0x0C, 0x04}, {0x80, 0x01, 0x00}} {
		if _, err := ParseHeadsetReport(r); err == nil {
			t.Errorf("report % X should be rejected", r)
		}
	}
}

func TestFrameRejectsOversizePayloads(t *testing.T) {
	if _, err := frameHeadsetReport(make([]byte, 63)); err == nil {
		t.Error("63-byte payload does not fit a 64-byte report")
	}
	if r, err := frameHeadsetReport(make([]byte, 62)); err != nil || len(r) != 64 {
		t.Errorf("62-byte payload should fit: %v", err)
	}
}

// ack builds the headset's acknowledgement of a SET: same event, TxID and param, type 0x20.
func ack(id byte, tx uint16, param ...byte) []byte {
	return padded(event(0x14, id, TypeAck, tx, param...))
}

func event2(addr, id, typ byte, tx uint16, param ...byte) []byte {
	return event(addr, id, typ, tx, param...)
}

func TestSetSendsTheAppsFrameAndWaitsForTheMatchingAck(t *testing.T) {
	f := &fakeRW{reads: []fakeRead{{data: ack(35, 1, 0x03, 0xFF)}}}
	got, err := newLink(f).Set(AddrRX, 35, []byte{0x03, 0xFF}, time.Second)
	if err != nil || got.EventType != TypeAck || got.TxID != 1 {
		t.Fatalf("ack=%+v err=%v", got, err)
	}
	sent, err := parseCommand(f.writes[0])
	if err != nil {
		t.Fatal(err)
	}
	// 01 00 fc 0a 96 c3 41 23 02 01 00 03 ff CKS : to the headset, event 35, type SET, TxID 1
	if sent.Address != 0x41 || sent.EventID != 35 || sent.EventType != FlagSET || sent.TxID != 1 || !bytes.Equal(sent.Param, []byte{0x03, 0xFF}) {
		t.Errorf("sent %+v", sent)
	}
}

// parseCommand decodes a host->device report: [02][len] 01 00 fc PLEN 96 c3 ADDR EVT TYPE TX_LO TX_HI PARAM CKS
// (PLEN = 8 + len(PARAM), counting the checksum), verifying the checksum independently of the code under test.
func parseCommand(report []byte) (*HciPacket, error) {
	payload := report[2 : 2+int(report[1])]
	if len(payload) < 12 || payload[0] != 0x01 || payload[1] != 0x00 || payload[2] != 0xFC {
		return nil, errors.New("not a command packet")
	}
	paramLen := int(payload[3]) - 8
	p := &HciPacket{Address: payload[6], EventID: payload[7], EventType: payload[8], TxID: uint16(payload[9]) | uint16(payload[10])<<8}
	p.Param = payload[11 : 11+paramLen]
	sum := 0
	for _, b := range payload[4 : 11+paramLen] {
		sum += int(b)
	}
	if byte(sum) != payload[11+paramLen] || len(payload) != 12+paramLen {
		return nil, errors.New("bad command checksum or length")
	}
	return p, nil
}

func TestSetNumbersTxIDsPerEvent(t *testing.T) {
	f := &fakeRW{reads: []fakeRead{{data: ack(35, 1, 1, 0xFF)}, {data: ack(35, 2, 2, 0xFF)}, {data: ack(34, 1, 0x32)}, {data: ack(35, 3, 3, 0xFF)}}}
	l := newLink(f)
	for i, call := range []struct {
		event byte
		param []byte
		tx    uint16
	}{{35, []byte{1, 0xFF}, 1}, {35, []byte{2, 0xFF}, 2}, {34, []byte{0x32}, 1}, {35, []byte{3, 0xFF}, 3}} {
		if _, err := l.Set(AddrRX, call.event, call.param, time.Second); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		sent, _ := parseCommand(f.writes[i])
		if sent.TxID != call.tx {
			t.Errorf("call %d (event %d): TxID %d, want %d", i, call.event, sent.TxID, call.tx)
		}
	}
}

func TestSetIgnoresNotificationsAndOtherAcks(t *testing.T) {
	f := &fakeRW{reads: []fakeRead{
		{data: padded(event(0x14, 65, TypeNotify, 1, 1, 10, 0xFF, 1))}, // the button-press style notification, same event
		{data: ack(65, 9, 1, 10, 0xFF, 1)},                             // an ack for some other SET (TxID 9)
		{data: padded(event(0x14, 65, TypeReply, 1, 1, 10, 0xFF, 1))},  // a GET reply is not an ack
		{data: ack(65, 1, 2, 10, 0xFF, 0)},                             // ours
	}}
	l := newLink(f)
	got, err := l.Set(AddrRX, 65, []byte{2, 10, 0xFF, 0}, time.Second)
	if err != nil || got.Param[0] != 2 || l.Skipped() != 3 {
		t.Fatalf("got=%+v err=%v skipped=%d", got, err, l.Skipped())
	}
}

func TestSetWithoutAnAckIsAnError(t *testing.T) {
	_, err := newLink(&fakeRW{}).Set(AddrRX, 35, []byte{1, 0xFF}, 50*time.Millisecond)
	if !errors.Is(err, ErrNoReply) || !strings.Contains(err.Error(), "acknowledgement") {
		t.Fatalf("want an ErrNoReply mentioning the acknowledgement, got %v", err)
	}
}

func TestTxIDWrapSkipsZero(t *testing.T) {
	l := newLink(&fakeRW{})
	l.tx = map[byte]uint16{7: 0xFFFF}
	if got := l.nextTx(7); got != 1 {
		t.Errorf("after 0xFFFF the next TxID should be 1, got %d", got)
	}
}
