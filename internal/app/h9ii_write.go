package app

import (
	"errors"
	"fmt"
	"time"

	"github.com/sstallion/go-hid"

	"github.com/patyhank/linzone-hub/internal/protocol"
	"github.com/patyhank/linzone-hub/internal/usb"
)

// INZONE H9 II writers. Every encoding below is what the Windows INZONE Hub sends (from
// captures of its traffic); the GUI keeps working in percent and these convert.

// h9iiAckTimeout is how long to wait for a SET's acknowledgement (they arrive within 5-80 ms).
const h9iiAckTimeout = 500 * time.Millisecond

type eventSetter interface {
	Set(dst byte, event byte, param []byte, timeout time.Duration) (*protocol.HciPacket, error)
}

func h9iiSetLink(l eventSetter, event byte, param []byte) error {
	_, err := l.Set(protocol.AddrRX, event, param, h9iiAckTimeout)
	return err
}

func h9iiSet(dev *hid.Device, event byte, param []byte) error {
	return h9iiSetLink(protocol.NewLink(dev), event, param)
}

// h9iiUnavailable is returned for controls that exist in this app but not on the H9 II over
// USB: the headset never answers their events (a full scan of all 256 IDs found no reply), and
// the Windows app does not use them either.
func h9iiUnavailable(what string) error {
	return fmt.Errorf("%s is not available on the INZONE H9 II over USB", what)
}

// --- parameter builders ---

// h9iiVolumeParam: [mute, step 0-30, step]. The headset stores the step verbatim and ignores
// the last byte; it is kept equal to the step, which is what has been verified on hardware.
func h9iiVolumeParam(percent int) []byte {
	level := h9IIVolumeFromPercent(percent)
	return []byte{0, level, level}
}

// h9iiSidetoneParam: [level 0-10, ff].
func h9iiSidetoneParam(percent int) []byte {
	return []byte{h9iiSidetoneFromPercent(percent), 0xFF}
}

// h9iiAmbientParam: [mode, level 1-20, ff, voice focus]. Mode is 0 off, 1 noise canceling,
// 2 ambient; the H9 II has no "custom" mode.
func h9iiAmbientParam(mode int, percent int, voiceFocus bool) ([]byte, error) {
	if mode < 0 || mode > 2 {
		return nil, fmt.Errorf("ambient mode must be 0-2 on the INZONE H9 II")
	}
	focus := byte(0)
	if voiceFocus {
		focus = 1
	}
	return []byte{byte(mode), h9iiAmbientFromPercent(percent), 0xFF, focus}, nil
}

// h9iiMixParam: [balance 0-100], 0 = all game, 100 = all chat.
func h9iiMixParam(balance int) ([]byte, error) {
	if balance < 0 || balance > 100 {
		return nil, fmt.Errorf("game/chat balance must be 0-100")
	}
	return []byte{byte(balance)}, nil
}

// h9iiAutoPowerOffMinutes are the durations confirmed from the Windows app. Others the generic
// code allows (5, 60) have never been seen on this headset, so they are not sent.
var h9iiAutoPowerOffMinutes = map[int]bool{0: true, 15: true, 30: true, 180: true}

const h9iiDefaultAutoOffSelection = 15

// h9iiAutoPowerOffParam: [minutes, selected]. Switching it on sends the same value twice
// (15 min = 0f 0f). Switching it off sends 0 and leaves the remembered selection alone
// (off = 00 0f when 15 minutes was selected), so the last selection is passed in.
func h9iiAutoPowerOffParam(minutes int, selected int) ([]byte, error) {
	if !h9iiAutoPowerOffMinutes[minutes] {
		return nil, fmt.Errorf("auto power off must be one of 0, 15, 30, 180 on the INZONE H9 II")
	}
	if minutes != 0 {
		return []byte{byte(minutes), byte(minutes)}, nil
	}
	if !h9iiAutoPowerOffMinutes[selected] || selected == 0 {
		selected = h9iiDefaultAutoOffSelection
	}
	return []byte{0, byte(selected)}, nil
}

// h9iiAutoPowerOff reads the current selection first so that switching the timer off does not
// forget which duration was selected.
func h9iiAutoPowerOff(l interface {
	eventQuerier
	eventSetter
}, minutes int) error {
	selected := h9iiDefaultAutoOffSelection
	if pkt, err := l.Query(protocol.AddrRX, protocol.EvtAutoPowerOff, h9iiQueryTimeout); err == nil && len(pkt.Param) >= 2 {
		selected = int(pkt.Param[1])
	}
	param, err := h9iiAutoPowerOffParam(minutes, selected)
	if err != nil {
		return err
	}
	return h9iiSetLink(l, protocol.EvtAutoPowerOff, param)
}

// forHeadset runs h9ii for an INZONE H9 II and generic for any other HID headset. A nil h9ii
// means the control does not exist on the H9 II, which is reported without touching the device.
func forHeadset(index int, label string, h9ii func(*protocol.Link) error, generic func(*hid.Device) error) error {
	if base, err := deviceByIndex(index); err == nil && usb.IsH9II(base.ProductID) && h9ii == nil {
		return h9iiUnavailable(label)
	}
	return withHeadset(index, func(dev *hid.Device, info DeviceSummary) error {
		if usb.IsH9II(info.ProductID) {
			if h9ii == nil {
				return h9iiUnavailable(label)
			}
			return h9ii(protocol.NewLink(dev))
		}
		if info.IsBuds {
			return errors.New(label + " command for INZONE Buds is not mapped yet")
		}
		return generic(dev)
	})
}
