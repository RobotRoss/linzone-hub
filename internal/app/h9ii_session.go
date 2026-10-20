package app

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/patyhank/linzone-hub/internal/protocol"
	"github.com/patyhank/linzone-hub/internal/usb"
)

// Names of the events the service emits to the frontend.
const (
	// EventStateChanged carries a StateEvent: one setting changed on the headset itself (a button,
	// the volume wheel, Bluetooth connecting...) or was echoed back after a SET.
	EventStateChanged = "inzone:state"
	// EventDeviceLost is emitted when the headset's HID handle failed (unplugged, for example).
	EventDeviceLost = "inzone:device-lost"
)

// StateEvent is the payload of EventStateChanged. State holds only the DeviceState fields that
// changed, with the same JSON names as DeviceState, so the frontend can merge it into what it has.
type StateEvent struct {
	Index int            `json:"index"`
	Event int            `json:"event"`
	State map[string]any `json:"state"`
}

// h9iiLink is what the H9 II state reader and writers need: Query and Set. Both *protocol.Link
// (one-shot, no listener) and *protocol.Session (persistent, with notifications) provide it.
type h9iiLink interface {
	eventQuerier
	eventSetter
}

// h9iiSession is a persistent protocol.Session on an H9 II plus what the service knows about it.
type h9iiSession struct {
	*protocol.Session
	info DeviceSummary
}

func (s *Service) isH9II(index int) bool {
	base, err := deviceByIndex(index)
	return err == nil && usb.IsH9II(base.ProductID)
}

// sessionFor returns the session for an H9 II, opening one if there is none or the last one
// died. The session stays open so that the headset's own notifications keep arriving; it is
// closed on rescan (ListDevices) or when the device fails.
func (s *Service) sessionFor(index int) (*h9iiSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur := s.sessions[index]; cur != nil {
		select {
		case <-cur.Done():
			delete(s.sessions, index)
			_ = cur.Close()
		default:
			return cur, nil
		}
	}
	base, err := deviceByIndex(index)
	if err != nil {
		return nil, err
	}
	if !usb.IsH9II(base.ProductID) {
		return nil, fmt.Errorf("%s is not an INZONE H9 II", base.Model)
	}
	hs, err := s.openH9IISession(index, base)
	if err != nil {
		return nil, err
	}
	if s.sessions == nil {
		s.sessions = map[int]*h9iiSession{}
	}
	s.sessions[index] = hs
	go s.watchH9IISession(index, hs)
	return hs, nil
}

// openH9IISession opens the control interface that answers the headset and starts a session on
// it. A headset that is switched off answers nothing on any interface; then the first interface
// that opens is kept anyway, so notifications flow once the headset comes back.
func (s *Service) openH9IISession(index int, base usb.DeviceInfo) (*h9iiSession, error) {
	candidates, err := usb.FindHeadsetControlInterfaces(base)
	if err != nil {
		return nil, err
	}
	notify := func(p *protocol.HciPacket) { s.handleH9IINotification(index, p) }
	var firstOpenable *usb.DeviceInfo
	var lastErr error
	tried := map[string]bool{}
	for _, candidate := range candidates {
		if tried[candidate.Path] {
			continue
		}
		tried[candidate.Path] = true
		dev, err := usb.Open(candidate)
		if err != nil {
			lastErr = fmt.Errorf("open %s %s: %w", candidate.Model, candidate.Path, err)
			continue
		}
		sess := protocol.NewSession(dev, notify)
		if _, err := sess.Query(protocol.AddrRX, protocol.EvtBatteryInfo, h9iiQueryTimeout); err == nil {
			return &h9iiSession{Session: sess, info: summarizeDevice(index, candidate)}, nil
		}
		_ = sess.Close()
		if firstOpenable == nil {
			c := candidate
			firstOpenable = &c
		}
	}
	if firstOpenable != nil {
		dev, err := usb.Open(*firstOpenable)
		if err != nil {
			return nil, fmt.Errorf("open %s %s: %w", firstOpenable.Model, firstOpenable.Path, err)
		}
		return &h9iiSession{Session: protocol.NewSession(dev, notify), info: summarizeDevice(index, *firstOpenable)}, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no usable headset control interface found for %s", base.Model)
}

// watchH9IISession drops a session whose device failed and tells the frontend. A session closed
// on purpose (rescan) is ignored.
func (s *Service) watchH9IISession(index int, hs *h9iiSession) {
	<-hs.Done()
	if hs.Stopped() {
		return
	}
	s.mu.Lock()
	if s.sessions[index] == hs {
		delete(s.sessions, index)
	}
	s.mu.Unlock()
	reason := ""
	if err := hs.Err(); err != nil {
		reason = err.Error()
	}
	_ = hs.Close()
	s.emitEvent(EventDeviceLost, map[string]any{"index": index, "error": reason})
}

// closeSessions closes every open session. Called when the device list is rescanned, because
// device indexes are only stable between scans.
func (s *Service) closeSessions() {
	s.mu.Lock()
	open := s.sessions
	s.sessions = nil
	s.mu.Unlock()
	for _, hs := range open {
		_ = hs.Close()
	}
}

func (s *Service) emitEvent(name string, payload any) {
	if s.emit != nil {
		s.emit(name, payload)
	}
}

// handleH9IINotification turns an unsolicited notification into a StateEvent. It runs on the
// session's reader goroutine, so it only decodes and emits.
func (s *Service) handleH9IINotification(index int, pkt *protocol.HciPacket) {
	patch, err := h9iiStatePatch(pkt.EventID, pkt.Param)
	if err != nil || patch == nil {
		return
	}
	s.emitEvent(EventStateChanged, StateEvent{Index: index, Event: int(pkt.EventID), State: patch})
}

// h9iiSetEvent sends one acknowledged SET to an H9 II over its session.
func (s *Service) h9iiSetEvent(index int, event byte, param []byte) error {
	hs, err := s.sessionFor(index)
	if err != nil {
		return err
	}
	return h9iiSetLink(hs, event, param)
}

// h9iiStatePatch decodes the parameters of one event (a GET reply and a notification share their
// layout) into the DeviceState fields it carries, as a JSON-shaped map. It returns nil for an
// event the GUI does not show.
func h9iiStatePatch(event byte, param []byte) (map[string]any, error) {
	for _, q := range h9iiQueries {
		if q.event != event {
			continue
		}
		st := &DeviceState{Warnings: []string{}}
		if err := q.apply(st, param); err != nil {
			return nil, fmt.Errorf("event %d: %w", event, err)
		}
		return statePatch(st)
	}
	return nil, nil
}

// statePatch is st as a JSON-shaped map without the fields that are not state (the device
// summary, warnings) and without empty containers.
func statePatch(st *DeviceState) (map[string]any, error) {
	raw, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	delete(m, "device")
	delete(m, "warnings")
	if b, ok := m["battery"].(map[string]any); ok && len(b) == 0 {
		delete(m, "battery")
	}
	if len(m) == 0 {
		return nil, errors.New("event carries no state")
	}
	return m, nil
}
