package app

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/patyhank/linzone-hub/internal/protocol"
)

func TestH9IISidetoneScale(t *testing.T) {
	for level := 0; level <= 10; level++ { // every headset step survives a round trip exactly
		pct := h9iiPercentFromSidetone(level)
		if got := h9iiSidetoneFromPercent(pct); int(got) != level {
			t.Errorf("level %d -> %d%% -> %d", level, pct, got)
		}
	}
	cases := map[int]byte{-5: 0, 0: 0, 4: 0, 5: 1, 50: 5, 94: 9, 95: 10, 100: 10, 250: 10}
	for pct, want := range cases {
		if got := h9iiSidetoneFromPercent(pct); got != want {
			t.Errorf("h9iiSidetoneFromPercent(%d) = %d, want %d", pct, got, want)
		}
	}
	if h9iiPercentFromSidetone(3) != 30 || h9iiPercentFromSidetone(99) != 100 || h9iiPercentFromSidetone(-1) != 0 {
		t.Error("percent-from-level wrong at sample points or not clamped")
	}
}

func TestH9IIAmbientScale(t *testing.T) {
	// The Windows app only ever uses levels 1-20, so nothing outside that may be produced.
	prev := byte(0)
	for pct := 0; pct <= 100; pct++ {
		level := h9iiAmbientFromPercent(pct)
		if level < h9iiAmbientMin || level > h9iiAmbientMax {
			t.Fatalf("%d%% -> level %d is outside 1-20", pct, level)
		}
		if level < prev {
			t.Fatalf("not monotonic at %d%%", pct)
		}
		prev = level
	}
	for level := h9iiAmbientMin; level <= h9iiAmbientMax; level++ { // every step survives a round trip
		if got := h9iiAmbientFromPercent(h9iiPercentFromAmbient(level)); int(got) != level {
			t.Errorf("level %d -> %d%% -> %d", level, h9iiPercentFromAmbient(level), got)
		}
	}
	// level 10 is the default the headset reported on every ANC-button press we captured
	if h9iiPercentFromAmbient(10) != 47 || h9iiPercentFromAmbient(1) != 0 || h9iiPercentFromAmbient(20) != 100 {
		t.Error("ambient percent wrong at sample points")
	}
	if h9iiPercentFromAmbient(0) != 0 || h9iiPercentFromAmbient(77) != 100 { // values the old 0-100 CLI range could store
		t.Error("out-of-range levels should clamp")
	}
}

func TestDecodersMatchTheDocumentedBytes(t *testing.T) {
	vol, err := decodeH9IIVolume([]byte{0x00, 0x1e, 0xff}) // wheel at its top
	if err != nil || vol.Raw != 30 || vol.Percent != 100 || vol.Mute != 0 {
		t.Errorf("volume: %+v %v", vol, err)
	}
	if v, _ := decodeH9IIVolume([]byte{0x00, 0x0f, 0xff}); v.Percent != 50 {
		t.Errorf("half volume reads %d%%", v.Percent)
	}
	for _, c := range []struct {
		param []byte
		mute  int
	}{{[]byte{0x00, 0xff, 0xff}, 0}, {[]byte{0x01, 0xff, 0xff}, 1}} { // boom attached / removed
		mic, err := decodeH9IIMic(c.param)
		if err != nil || mic.Mute != c.mute || mic.Percent != 0 {
			t.Errorf("mic % X: %+v %v", c.param, mic, err)
		}
	}
	// a real level, should a headset ever report one: percent byte wins, else the value byte
	if m, _ := decodeH9IIMic([]byte{0x00, 0x20, 0x40}); m.Percent != 0x40 || m.Mute != 0 {
		t.Errorf("mic with percent byte: %+v", m)
	}
	if m, _ := decodeH9IIMic([]byte{0x00, 0x20, 0xff}); m.Percent != 0x20 {
		t.Errorf("mic with only a value byte: %+v", m)
	}
	for _, c := range []struct {
		param    []byte
		attached bool
	}{{[]byte{0x00}, true}, {[]byte{0x01}, false}} { // event 143: 00 = connected, 01 = removed
		got, err := decodeH9IIMicAttached(c.param)
		if err != nil || got != c.attached {
			t.Errorf("mic attached % X = %v %v, want %v", c.param, got, err, c.attached)
		}
	}
	if _, err := decodeH9IIMicAttached(nil); err == nil {
		t.Error("an empty mic attached payload must be an error")
	}
	amb, err := decodeH9IIAmbient([]byte{0x01, 0x0a, 0xff, 0x01}) // NC, level 10, voice focus on
	if err != nil || amb.Mode != 1 || amb.AmbientRaw != 10 || amb.AmbientPercent != 47 || !amb.VoiceFocus {
		t.Errorf("ambient: %+v %v", amb, err)
	}
	if a, _ := decodeH9IIAmbient([]byte{0x02, 0x14, 0xff, 0x00}); a.Mode != 2 || a.AmbientPercent != 100 || a.VoiceFocus {
		t.Errorf("ambient max: %+v", a)
	}
	side, err := decodeH9IISidetone([]byte{0x03, 0xff})
	if err != nil || side.Raw != 3 || side.Percent != 30 {
		t.Errorf("sidetone: %+v %v", side, err)
	}
	for name, fn := range map[string]func() error{
		"volume":   func() error { _, e := decodeH9IIVolume([]byte{0, 1}); return e },
		"mic":      func() error { _, e := decodeH9IIMic(nil); return e },
		"ambient":  func() error { _, e := decodeH9IIAmbient([]byte{1, 2, 3}); return e },
		"sidetone": func() error { _, e := decodeH9IISidetone(nil); return e },
	} {
		if fn() == nil {
			t.Errorf("%s: a short payload must be an error", name)
		}
	}
}

func TestSerialIsReadFromTheModelReply(t *testing.T) {
	// header 05 01 ff ff ff 00, two NUL-terminated copies of the serial, padding, tail 01 ff 3f
	reply, _ := hex.DecodeString("0501ffffff00" + hex.EncodeToString([]byte("2004224\x002004224\x00")) + "0000000000" + "01ff3f")
	if serial, ok := h9iiSerial(reply); !ok || serial != 2004224 {
		t.Errorf("serial = %d, %v", serial, ok)
	}
	for name, p := range map[string][]byte{
		"empty":             nil,
		"header only":       {5, 1, 0xff, 0xff, 0xff, 0},
		"not terminated":    append([]byte{5, 1, 0xff, 0xff, 0xff, 0}, []byte("2004224")...),
		"not a number":      append([]byte{5, 1, 0xff, 0xff, 0xff, 0}, []byte("ABC\x00")...),
		"starts with NUL":   {5, 1, 0xff, 0xff, 0xff, 0, 0, '1', 0},
		"binary where text": {5, 1, 0xff, 0xff, 0xff, 0, 0xfe, 0xfe, 0},
	} {
		if serial, ok := h9iiSerial(p); ok {
			t.Errorf("%s: should not parse, got %d", name, serial)
		}
	}
}

// fakeQuerier plays a headset: it answers the events in replies and stays silent for the rest.
type fakeQuerier struct {
	replies map[byte][]byte
	errs    map[byte]error
	events  []byte
	dsts    []byte
}

func (f *fakeQuerier) Query(dst byte, event byte, _ time.Duration) (*protocol.HciPacket, error) {
	f.events = append(f.events, event)
	f.dsts = append(f.dsts, dst)
	if err := f.errs[event]; err != nil {
		return nil, err
	}
	p, ok := f.replies[event]
	if !ok {
		return nil, fmt.Errorf("event %d: %w", event, protocol.ErrNoReply)
	}
	return &protocol.HciPacket{EventID: event, EventType: protocol.TypeReply, Param: p}, nil
}

func h9iiReplies() map[byte][]byte {
	model, _ := hex.DecodeString("0501ffffff00" + hex.EncodeToString([]byte("2004224\x002004224\x00")) + "01ff3f")
	return map[byte][]byte{
		2: model, 3: {1, 1, 0, 0, 1, 1, 0, 0}, 4: {0x00, 0x5e},
		33: {0x00, 0x1e, 0xff}, 36: {0x01, 0xff, 0xff}, 143: {0x00}, 65: {0x01, 0x0a, 0xff, 0x01},
		34: {0x32}, 35: {0x03, 0xff}, 97: {0x01, 0x01}, 99: {0x01}, 129: {0x1e, 0x1e}, 67: {0x01},
	}
}

func TestReadH9IIStateFillsEverythingFromIndividualEvents(t *testing.T) {
	q := &fakeQuerier{replies: h9iiReplies()}
	state := &DeviceState{Warnings: []string{}}
	readH9IIState(q, state)

	if len(state.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", state.Warnings)
	}
	if state.Battery.Headset == nil || state.Battery.Headset.Percent != 94 || state.Battery.Headset.Status != 0 {
		t.Errorf("battery: %+v", state.Battery.Headset)
	}
	if state.SerialNumber == nil || *state.SerialNumber != 2004224 || *state.ModelID != 5 {
		t.Errorf("model: serial=%v model=%v", state.SerialNumber, state.ModelID)
	}
	if state.FirmwareRaw != "0101000001010000" {
		t.Errorf("firmware %q", state.FirmwareRaw)
	}
	// boom attached (143 = 00) while the mic is muted (36 = 01): the mute button, not a removed boom
	if state.MicAttached == nil || !*state.MicAttached || state.MicVolume.Mute != 1 {
		t.Errorf("attached=%v mic=%+v", state.MicAttached, state.MicVolume)
	}
	if state.HeadphoneVolume.Percent != 100 || state.Sidetone.Percent != 30 {
		t.Errorf("volume=%+v mic=%+v sidetone=%+v", state.HeadphoneVolume, state.MicVolume, state.Sidetone)
	}
	if a := state.Ambient; a.Mode != 1 || a.AmbientRaw != 10 || a.AmbientPercent != 47 || !a.VoiceFocus {
		t.Errorf("ambient: %+v", a)
	}
	for name, s := range map[string]*SimpleState{"mix": state.GameChatMix, "bt": state.BTStatus, "btStartup": state.BTStartupMode, "autoOff": state.AutoPowerOff, "ncStartup": state.NCStartupMode} {
		if s == nil || s.Value == nil {
			t.Errorf("%s not filled", name)
		}
	}
	if *state.GameChatMix.Value != 50 || *state.AutoPowerOff.Value != 30 || state.BTStatus.Raw[1] != 1 {
		t.Errorf("simple states: mix=%d autoOff=%d bt=%v", *state.GameChatMix.Value, *state.AutoPowerOff.Value, state.BTStatus.Raw)
	}
}

func TestReadH9IIStateAsksOnlyForEventsTheHeadsetAnswers(t *testing.T) {
	q := &fakeQuerier{replies: h9iiReplies()}
	readH9IIState(q, &DeviceState{})

	// every one of these is among the 19 IDs the headset answered in the full scan
	answers := map[byte]bool{2: true, 3: true, 4: true, 6: true, 7: true, 8: true, 33: true, 34: true, 35: true, 36: true, 65: true, 66: true, 67: true, 97: true, 99: true, 129: true, 131: true, 132: true, 143: true}
	for _, ev := range q.events {
		if !answers[ev] {
			t.Errorf("queried event %d, which the headset never answers", ev)
		}
	}
	for _, silent := range []byte{1, 37, 98, 133, 141} { // the five that cost 0.6-1.2 s each before
		for _, ev := range q.events {
			if ev == silent {
				t.Errorf("event %d must not be queried", silent)
			}
		}
	}
	for _, d := range q.dsts {
		if d != protocol.AddrRX {
			t.Errorf("queried address %d, want the headset (%d)", d, protocol.AddrRX)
		}
	}
	if len(q.events) != 13 {
		t.Errorf("want 13 queries, got %d: %v", len(q.events), q.events)
	}
}

func TestReadH9IIStateStopsAtOnceWhenTheHeadsetIsSilent(t *testing.T) {
	q := &fakeQuerier{replies: map[byte][]byte{}} // headset off: nothing answers
	state := &DeviceState{Warnings: []string{}}
	readH9IIState(q, state)
	if len(q.events) != 1 {
		t.Errorf("a silent headset should cost one wait, not %d", len(q.events))
	}
	if len(state.Warnings) != 1 || !strings.Contains(state.Warnings[0], "switched on and connected") {
		t.Errorf("warnings: %v", state.Warnings)
	}
}

func TestReadH9IIStateSurvivesOneMissedReply(t *testing.T) {
	r := h9iiReplies()
	delete(r, 35) // one transient miss in the middle
	q := &fakeQuerier{replies: r}
	state := &DeviceState{Warnings: []string{}}
	readH9IIState(q, state)
	if len(q.events) != 13 || state.Sidetone != nil || state.BTStatus == nil || state.NCStartupMode == nil {
		t.Errorf("one miss should not stop the refresh: calls=%d sidetone=%v bt=%v", len(q.events), state.Sidetone, state.BTStatus)
	}
	if len(state.Warnings) != 1 || !strings.Contains(state.Warnings[0], "event 35") {
		t.Errorf("warnings: %v", state.Warnings)
	}
}

func TestReadH9IIStateGivesUpAfterConsecutiveMisses(t *testing.T) {
	r := h9iiReplies()
	delete(r, 35)
	delete(r, 97) // two in a row: the headset has gone away mid-refresh
	q := &fakeQuerier{replies: r}
	state := &DeviceState{Warnings: []string{}}
	readH9IIState(q, state)
	if last := q.events[len(q.events)-1]; last != 97 {
		t.Errorf("should stop at the second consecutive miss (event 97), stopped after %d", last)
	}
	if state.BTStartupMode != nil || state.AutoPowerOff != nil {
		t.Error("nothing after the give-up point should have been read")
	}
	if !strings.Contains(strings.Join(state.Warnings, "|"), "stopped answering") {
		t.Errorf("warnings: %v", state.Warnings)
	}
}

func TestReadH9IIStateStopsOnAnIOError(t *testing.T) {
	q := &fakeQuerier{replies: h9iiReplies(), errs: map[byte]error{36: errors.New("event 36: write: device gone")}}
	state := &DeviceState{Warnings: []string{}}
	readH9IIState(q, state)
	if last := q.events[len(q.events)-1]; last != 36 || len(state.Warnings) != 1 {
		t.Errorf("an I/O error is not a missed reply; last=%d warnings=%v", last, state.Warnings)
	}
}

func TestReadH9IIStateWarnsOnAGarbledPayloadAndCarriesOn(t *testing.T) {
	r := h9iiReplies()
	r[33] = []byte{0x00} // too short for a volume reply
	q := &fakeQuerier{replies: r}
	state := &DeviceState{Warnings: []string{}}
	readH9IIState(q, state)
	if state.HeadphoneVolume != nil || state.Ambient == nil || len(state.Warnings) != 1 || !strings.Contains(state.Warnings[0], "event 33") {
		t.Errorf("volume=%v ambient=%v warnings=%v", state.HeadphoneVolume, state.Ambient, state.Warnings)
	}
}

func TestReadH9IIStateReportsARemovedBoom(t *testing.T) {
	r := h9iiReplies()
	r[143] = []byte{0x01} // boom removed; the headset follows with mute on as well
	q := &fakeQuerier{replies: r}
	state := &DeviceState{Warnings: []string{}}
	readH9IIState(q, state)
	if state.MicAttached == nil || *state.MicAttached {
		t.Errorf("a removed boom must read as detached, got %v", state.MicAttached)
	}
	if len(state.Warnings) != 0 {
		t.Errorf("warnings: %v", state.Warnings)
	}
}
