package app

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/patyhank/linzone-hub/internal/protocol"
)

// INZONE H9 II state reader.
//
// Everything here follows captures of the Windows INZONE Hub's traffic and a full scan of every
// event ID against the headset: the headset answers GETs for only 19 event IDs, so only those
// are asked for. Queries to the headset go to AddrRX; the transceiver's event 1 is deliberately not
// queried because nothing in the GUI shows it yet.

// h9iiQueryTimeout is generous: a healthy reply takes 20-35 ms.
const h9iiQueryTimeout = 300 * time.Millisecond

// h9iiMaxConsecutiveMisses stops a refresh early once the headset has stopped answering
// (switched off, out of range), instead of waiting out every remaining query.
const h9iiMaxConsecutiveMisses = 2

// eventQuerier is the part of *protocol.Link the reader needs.
type eventQuerier interface {
	Query(dst byte, event byte, timeout time.Duration) (*protocol.HciPacket, error)
}

// Value ranges of the H9 II as used by the Windows app. The GUI keeps working in percent, so
// these convert between that and the headset's own steps.
const (
	h9iiSidetoneMax = 10 // sidetone level 0-10
	h9iiAmbientMin  = 1  // ambient level 1-20
	h9iiAmbientMax  = 20
)

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// h9iiSidetoneFromPercent maps 0-100% to the headset's sidetone level 0-10.
func h9iiSidetoneFromPercent(percent int) byte {
	return byte((clampInt(percent, 0, 100)*h9iiSidetoneMax + 50) / 100)
}

// h9iiPercentFromSidetone maps the headset's sidetone level 0-10 to 0-100%.
func h9iiPercentFromSidetone(level int) int {
	return clampInt(level, 0, h9iiSidetoneMax) * 100 / h9iiSidetoneMax
}

// h9iiAmbientFromPercent maps 0-100% to the headset's ambient level 1-20.
func h9iiAmbientFromPercent(percent int) byte {
	return byte(h9iiAmbientMin + (clampInt(percent, 0, 100)*(h9iiAmbientMax-h9iiAmbientMin)+50)/100)
}

// h9iiPercentFromAmbient maps the headset's ambient level 1-20 to 0-100%.
func h9iiPercentFromAmbient(level int) int {
	span := h9iiAmbientMax - h9iiAmbientMin
	return ((clampInt(level, h9iiAmbientMin, h9iiAmbientMax)-h9iiAmbientMin)*100 + span/2) / span
}

// --- decoders (pure functions of the reply parameters) ---

func decodeH9IIVolume(p []byte) (*VolumeState, error) { // [mute, value 0-30, ff]
	if len(p) < 3 {
		return nil, fmt.Errorf("volume payload too short")
	}
	return &VolumeState{Mute: int(p[0]), Raw: int(p[1]), Percent: h9IIPercentFromVolume(int(p[1]))}, nil
}

// decodeH9IIMic decodes event 36, [mute, value, percent]. Every capture so far has value and
// percent at ff ("not applicable"), so in practice only the mute flag carries information and
// Percent stays 0. If a headset ever reports a real level (value or percent <= 100) it is
// passed through, preferring the percent byte as the Buds decoder does.
func decodeH9IIMic(p []byte) (*VolumeState, error) {
	if len(p) < 1 {
		return nil, fmt.Errorf("mic payload too short")
	}
	v := &VolumeState{Mute: int(p[0])}
	if len(p) > 1 {
		v.Raw = int(p[1])
	}
	switch {
	case len(p) > 2 && p[2] <= 100:
		v.Percent = int(p[2])
	case len(p) > 1 && p[1] <= 100:
		v.Percent = int(p[1])
	}
	return v, nil
}

// decodeH9IIMicAttached decodes event 143: 00 = boom mic attached, 01 = removed.
func decodeH9IIMicAttached(p []byte) (bool, error) {
	if len(p) < 1 {
		return false, fmt.Errorf("mic attached payload too short")
	}
	return p[0] == 0, nil
}

func decodeH9IIAmbient(p []byte) (*AmbientState, error) { // [mode, level 1-20, ff, focus]
	if len(p) < 4 {
		return nil, fmt.Errorf("ambient payload too short")
	}
	return &AmbientState{Mode: int(p[0]), AmbientRaw: int(p[1]), AmbientPercent: h9iiPercentFromAmbient(int(p[1])), VoiceFocus: p[3] != 0}, nil
}

func decodeH9IISidetone(p []byte) (*VolumeState, error) { // [level 0-10, ff]
	if len(p) < 1 {
		return nil, fmt.Errorf("sidetone payload too short")
	}
	return &VolumeState{Raw: int(p[0]), Percent: h9iiPercentFromSidetone(int(p[0]))}, nil
}

// simpleStateFromParam is the generic "first byte is the value" decoding used by most settings.
func simpleStateFromParam(eventID byte, param []byte) *SimpleState {
	raw := make([]int, len(param))
	for i, b := range param {
		raw[i] = int(b)
	}
	var value *int
	if len(raw) > 0 {
		value = intPtr(raw[0])
	}
	return &SimpleState{EventID: int(eventID), Value: value, Raw: raw}
}

// h9iiSerial extracts the device serial from a model-info reply: after the 6-byte header come
// NUL-terminated ASCII strings, the first being the serial (the middle part of the label's
// S01-2004224-0).
func h9iiSerial(param []byte) (int, bool) {
	const header = 6
	if len(param) <= header {
		return 0, false
	}
	end := header
	for end < len(param) && param[end] >= 0x20 && param[end] <= 0x7e {
		end++
	}
	if end == header || end >= len(param) || param[end] != 0 {
		return 0, false
	}
	serial, err := strconv.Atoi(string(param[header:end]))
	if err != nil {
		return 0, false
	}
	return serial, true
}

// --- reader ---

type h9iiQuery struct {
	event byte
	apply func(*DeviceState, []byte) error
}

// h9iiQueries is every setting the GUI shows that the headset will answer, in the order read.
// Deliberately absent: 37 (surround), 98 (BT sound quality), 133 (connection destination) and
// 141 (assignable buttons), which never answer over USB, and the bulk events 6/7/8, whose
// layout is still being worked out (the individual events below are fully confirmed).
var h9iiQueries = []h9iiQuery{
	{protocol.EvtModelInfo, func(s *DeviceState, p []byte) error {
		m, err := protocol.ParseModelInfo(p)
		if err != nil {
			return err
		}
		s.ModelID = intPtr(int(m.ModelID))
		s.Color = intPtr(int(m.Color))
		s.SerialNumber = intPtr(int(m.Serial))
		s.Status = intPtr(int(m.Status))
		if serial, ok := h9iiSerial(p); ok {
			s.SerialNumber = intPtr(serial)
		}
		return nil
	}},
	{protocol.EvtFWVersion, func(s *DeviceState, p []byte) error { s.FirmwareRaw = hex.EncodeToString(p); return nil }},
	{protocol.EvtBatteryInfo, func(s *DeviceState, p []byte) error {
		b, err := protocol.ParseBatteryInfo(p)
		if err != nil {
			return err
		}
		s.Battery.Headset = batteryCell(b.Percent, b.Status)
		return nil
	}},
	{protocol.EvtHeadphoneVolume, func(s *DeviceState, p []byte) (err error) { s.HeadphoneVolume, err = decodeH9IIVolume(p); return }},
	{protocol.EvtMicVolume, func(s *DeviceState, p []byte) (err error) { s.MicVolume, err = decodeH9IIMic(p); return }},
	{protocol.EvtMicAttached, func(s *DeviceState, p []byte) error {
		attached, err := decodeH9IIMicAttached(p)
		if err != nil {
			return err
		}
		s.MicAttached = &attached
		return nil
	}},
	{protocol.EvtAmbSetting, func(s *DeviceState, p []byte) (err error) { s.Ambient, err = decodeH9IIAmbient(p); return }},
	{protocol.EvtGameChatMix, func(s *DeviceState, p []byte) error {
		s.GameChatMix = simpleStateFromParam(protocol.EvtGameChatMix, p)
		return nil
	}},
	{protocol.EvtSidetoneVolume, func(s *DeviceState, p []byte) (err error) { s.Sidetone, err = decodeH9IISidetone(p); return }},
	{protocol.EvtBTStatus, func(s *DeviceState, p []byte) error {
		s.BTStatus = simpleStateFromParam(protocol.EvtBTStatus, p)
		return nil
	}},
	{protocol.EvtBTStartupMode, func(s *DeviceState, p []byte) error {
		s.BTStartupMode = simpleStateFromParam(protocol.EvtBTStartupMode, p)
		return nil
	}},
	{protocol.EvtAutoPowerOff, func(s *DeviceState, p []byte) error {
		s.AutoPowerOff = simpleStateFromParam(protocol.EvtAutoPowerOff, p)
		return nil
	}},
	{protocol.EvtNCStartupMode, func(s *DeviceState, p []byte) error {
		s.NCStartupMode = simpleStateFromParam(protocol.EvtNCStartupMode, p)
		return nil
	}},
}

// readH9IIState fills state from the headset. A headset that is off or out of range stops
// answering, so a miss on the very first query (or two misses in a row) ends the refresh with
// a warning rather than waiting out the remaining queries.
func readH9IIState(q eventQuerier, state *DeviceState) {
	misses := 0
	for i, query := range h9iiQueries {
		pkt, err := q.Query(protocol.AddrRX, query.event, h9iiQueryTimeout)
		if err != nil {
			if !errors.Is(err, protocol.ErrNoReply) {
				state.Warnings = append(state.Warnings, err.Error())
				return
			}
			misses++
			if i == 0 {
				state.Warnings = append(state.Warnings, "the H9 II did not answer; is the headset switched on and connected to the transmitter?")
				return
			}
			state.Warnings = append(state.Warnings, err.Error())
			if misses >= h9iiMaxConsecutiveMisses {
				state.Warnings = append(state.Warnings, "the H9 II stopped answering; the remaining settings were not read")
				return
			}
			continue
		}
		misses = 0
		if err := query.apply(state, pkt.Param); err != nil {
			state.Warnings = append(state.Warnings, fmt.Sprintf("event %d: %v", query.event, err))
		}
	}
}
