package app

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/patyhank/linzone-hub/internal/protocol"
)

func TestH9IIParamsMatchTheWindowsAppsEncodings(t *testing.T) {
	eq := func(name string, got []byte, want ...byte) {
		t.Helper()
		if !bytes.Equal(got, want) {
			t.Errorf("%s = % X, want % X", name, got, want)
		}
	}
	eq("volume 50%", h9iiVolumeParam(50), 0, 15, 15)
	eq("volume 100%", h9iiVolumeParam(100), 0, 30, 30)

	// the captured slider: 0 gives 00 ff, 100 gives 0a ff, never anything past 10, always ff
	eq("sidetone 0%", h9iiSidetoneParam(0), 0x00, 0xFF)
	eq("sidetone 30%", h9iiSidetoneParam(30), 0x03, 0xFF)
	eq("sidetone 100%", h9iiSidetoneParam(100), 0x0A, 0xFF)
	for pct := -10; pct <= 130; pct++ {
		p := h9iiSidetoneParam(pct)
		if p[0] > 10 || p[1] != 0xFF {
			t.Fatalf("sidetone %d%% -> % X leaves the 0-10 / ff encoding", pct, p)
		}
	}

	p, err := h9iiAmbientParam(1, 47, true)
	if err != nil {
		t.Fatal(err)
	}
	eq("ambient NC 47% focus", p, 0x01, 0x0A, 0xFF, 0x01) // exactly what the headset reported back for level 10
	p, _ = h9iiAmbientParam(2, 100, false)
	eq("ambient ambient 100%", p, 0x02, 0x14, 0xFF, 0x00)
	p, _ = h9iiAmbientParam(0, 0, false)
	eq("ambient off 0%", p, 0x00, 0x01, 0xFF, 0x00)
	for pct := -5; pct <= 120; pct++ { // the old 0-100 range wrote 0x4d and 0x11, which the app never sends
		p, _ := h9iiAmbientParam(1, pct, false)
		if p[1] < 1 || p[1] > 20 || p[2] != 0xFF {
			t.Fatalf("ambient %d%% -> % X leaves the 1-20 / ff encoding", pct, p)
		}
	}
	if _, err := h9iiAmbientParam(3, 50, false); err == nil {
		t.Error("the H9 II has no custom ambient mode")
	}

	m, err := h9iiMixParam(25)
	eq("mix 25", m, 25)
	if err != nil {
		t.Error(err)
	}
	for _, bad := range []int{-1, 101} {
		if _, err := h9iiMixParam(bad); err == nil {
			t.Errorf("balance %d should be rejected", bad)
		}
	}
}

func TestH9IIAutoPowerOffEncoding(t *testing.T) {
	cases := []struct {
		minutes, selected int
		want              []byte
	}{
		{15, 0, []byte{0x0F, 0x0F}},   // 15 min = 0f 0f
		{30, 15, []byte{0x1E, 0x1E}},  // 30 = 1e 1e
		{180, 15, []byte{0xB4, 0xB4}}, // 3 hrs = b4 b4
		{0, 15, []byte{0x00, 0x0F}},   // off = 00 0f, selection stays at 15
		{0, 30, []byte{0x00, 0x1E}},   // ...and stays at whatever it was
		{0, 0, []byte{0x00, 0x0F}},    // nothing remembered: default to 15
		{0, 7, []byte{0x00, 0x0F}},    // a selection that is not one of the real options is not echoed
	}
	for _, c := range cases {
		got, err := h9iiAutoPowerOffParam(c.minutes, c.selected)
		if err != nil || !bytes.Equal(got, c.want) {
			t.Errorf("(%d, selected %d) = % X %v, want % X", c.minutes, c.selected, got, err, c.want)
		}
	}
	for _, bad := range []int{5, 60, -1, 240} {
		if _, err := h9iiAutoPowerOffParam(bad, 15); err == nil {
			t.Errorf("%d minutes was never seen on this headset and must not be sent", bad)
		}
	}
}

type fakeSetter struct {
	queryReply []byte // reply to the auto-power-off query; nil = no reply
	setErr     error
	sets       [][]byte
	events     []byte
}

func (f *fakeSetter) Query(dst byte, event byte, _ time.Duration) (*protocol.HciPacket, error) {
	if f.queryReply == nil {
		return nil, fmt.Errorf("event %d: %w", event, protocol.ErrNoReply)
	}
	return &protocol.HciPacket{EventID: event, EventType: protocol.TypeReply, Param: f.queryReply}, nil
}

func (f *fakeSetter) Set(dst byte, event byte, param []byte, _ time.Duration) (*protocol.HciPacket, error) {
	if dst != protocol.AddrRX {
		return nil, fmt.Errorf("SET sent to %d, not the headset", dst)
	}
	f.events = append(f.events, event)
	f.sets = append(f.sets, append([]byte(nil), param...))
	return &protocol.HciPacket{EventID: event, EventType: protocol.TypeAck, Param: param}, f.setErr
}

func TestH9IIAutoPowerOffRemembersTheSelectionWhenSwitchingOff(t *testing.T) {
	f := &fakeSetter{queryReply: []byte{0x1E, 0x1E}} // currently 30 min
	if err := h9iiAutoPowerOff(f, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(f.sets[0], []byte{0x00, 0x1E}) || f.events[0] != protocol.EvtAutoPowerOff {
		t.Errorf("sent % X to event %d; off should keep the 30 min selection", f.sets[0], f.events[0])
	}

	f = &fakeSetter{} // the read-back gets no reply: still switches off, with the default selection
	if err := h9iiAutoPowerOff(f, 0); err != nil || !bytes.Equal(f.sets[0], []byte{0x00, 0x0F}) {
		t.Errorf("err=%v sent=% X", err, f.sets)
	}

	f = &fakeSetter{queryReply: []byte{0x00, 0x0F}}
	if err := h9iiAutoPowerOff(f, 180); err != nil || !bytes.Equal(f.sets[0], []byte{0xB4, 0xB4}) {
		t.Errorf("err=%v sent=% X", err, f.sets)
	}

	f = &fakeSetter{}
	if err := h9iiAutoPowerOff(f, 60); err == nil || len(f.sets) != 0 {
		t.Errorf("an unsupported duration must be rejected before anything is sent: err=%v sets=%v", err, f.sets)
	}

	boom := errors.New("event 129: no acknowledgement: timed out")
	f = &fakeSetter{queryReply: []byte{0x0F, 0x0F}, setErr: boom}
	if err := h9iiAutoPowerOff(f, 30); !errors.Is(err, boom) {
		t.Errorf("a missing ack must surface, got %v", err)
	}
}

func TestSetterSendsToTheHeadset(t *testing.T) {
	f := &fakeSetter{}
	if err := h9iiSetLink(f, protocol.EvtSidetoneVolume, []byte{3, 0xFF}); err != nil || f.events[0] != protocol.EvtSidetoneVolume {
		t.Fatalf("err=%v events=%v", err, f.events)
	}
}

func TestUnavailableMessageNamesTheControl(t *testing.T) {
	err := h9iiUnavailable("surround")
	if !strings.Contains(err.Error(), "surround") || !strings.Contains(err.Error(), "H9 II") {
		t.Errorf("message: %v", err)
	}
}
