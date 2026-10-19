package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	hid "github.com/sstallion/go-hid"
)

// Message types of the INZONE HCI-over-HID protocol, as seen from the host. FlagRET and
// FlagNTFY keep their historical names above; per captures of the Windows INZONE Hub, 0x20 is
// the acknowledgement of a SET (not a notification), and unsolicited state notifications are
// type 0xA0.
const (
	TypeReply  = FlagRET  // 0x10: reply to a GET
	TypeAck    = FlagNTFY // 0x20: acknowledgement of a SET (echoes TxID and param)
	TypeNotify = 0xA0     // unsolicited state notification, always TxID 1
)

const (
	// linkReportID is the report ID of headset-framed HCI traffic: [02][len][HCI packet][padding].
	linkReportID = 0x02
	// linkReportSize is the fixed size of an output report in the headset framing.
	linkReportSize = 64
	// linkQueryTxID is the TxID the Windows app uses for every GET.
	linkQueryTxID = 1
)

// ErrNoReply is returned (wrapped) by Link.Query and Link.Set when no matching reply or
// acknowledgement arrives in time.
var ErrNoReply = errors.New("timed out")

// reportIO is the part of *hid.Device a Link needs, as an interface so it can be faked in tests.
type reportIO interface {
	reportReader
	Write(p []byte) (int, error)
}

// Link is a request/reply channel to an INZONE headset in the headset framing. It always
// addresses packets the way the Windows app does (destination and source in the address byte,
// the app's TxID), and matches a GET's reply on event ID, type, TxID and source, so unsolicited
// notifications and other traffic on the same node are skipped rather than mistaken for it.
//
// A Link is not safe for concurrent use.
type Link struct {
	rw      reportIO
	skipped int
	tx      map[byte]uint16 // last TxID used per event
}

// NewLink returns a Link over an open headset HID handle.
func NewLink(dev *hid.Device) *Link { return &Link{rw: dev} }

func newLink(rw reportIO) *Link { return &Link{rw: rw} }

// Skipped is how many received reports Query has set aside as not being the awaited reply
// (notifications, replies to other queries, non-HCI reports such as media keys).
func (l *Link) Skipped() int { return l.skipped }

// frameHeadsetReport wraps an HCI packet in the headset framing.
func frameHeadsetReport(payload []byte) ([]byte, error) {
	if len(payload) > linkReportSize-2 {
		return nil, fmt.Errorf("hci payload too large: %d (max %d)", len(payload), linkReportSize-2)
	}
	report := make([]byte, linkReportSize)
	report[0] = linkReportID
	report[1] = byte(len(payload))
	copy(report[2:], payload)
	return report, nil
}

// Query sends a GET for event to dst (AddrRX = the headset, AddrTX = the USB transceiver)
// and returns its reply. It returns an error wrapping ErrNoReply if none arrives within timeout.
func (l *Link) Query(dst byte, event byte, timeout time.Duration) (*HciPacket, error) {
	payload := BuildHciGetWithAddress(event, byte(dst<<4|AddrPC), linkQueryTxID, nil)
	return l.exchange(dst, event, payload, timeout, "reply", func(p *HciPacket) bool {
		return p.EventType == TypeReply && p.TxID == linkQueryTxID
	})
}

// Set sends a SET for event to dst and waits for the headset's acknowledgement, which echoes
// the TxID. Like the Windows app it numbers SETs per event, starting at 1 and counting up, so
// an acknowledgement can only belong to the SET that was just sent. It returns an error
// wrapping ErrNoReply if no acknowledgement arrives within timeout, in which case the change
// may or may not have been applied.
func (l *Link) Set(dst byte, event byte, param []byte, timeout time.Duration) (*HciPacket, error) {
	txid := l.nextTx(event)
	payload := BuildHciSetWithAddress(event, byte(dst<<4|AddrPC), txid, param)
	return l.exchange(dst, event, payload, timeout, "acknowledgement", func(p *HciPacket) bool {
		return p.EventType == TypeAck && p.TxID == txid
	})
}

// nextTx returns the next TxID for event: 1, 2, 3, ... skipping 0 when it wraps.
func (l *Link) nextTx(event byte) uint16 {
	if l.tx == nil {
		l.tx = map[byte]uint16{}
	}
	n := l.tx[event] + 1
	if n == 0 {
		n = 1
	}
	l.tx[event] = n
	return n
}

// exchange writes one packet and returns the first received event that is from the node that
// was addressed, carries event's ID and satisfies match. Everything else is set aside.
func (l *Link) exchange(dst byte, event byte, payload []byte, timeout time.Duration, what string, match func(*HciPacket) bool) (*HciPacket, error) {
	report, err := frameHeadsetReport(payload)
	if err != nil {
		return nil, err
	}
	if _, err := l.rw.Write(report); err != nil {
		return nil, fmt.Errorf("event %d: write: %w", event, err)
	}

	wantFrom := byte(AddrPC<<4 | dst)
	deadline := time.Now().Add(timeout)
	buf := make([]byte, linkReportSize)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("event %d: no %s: %w", event, what, ErrNoReply)
		}
		n, err := ReadReport(l.rw, buf, remaining)
		if errors.Is(err, hid.ErrTimeout) {
			return nil, fmt.Errorf("event %d: no %s: %w", event, what, ErrNoReply)
		}
		if err != nil {
			return nil, fmt.Errorf("event %d: read: %w", event, err)
		}
		pkt, err := ParseHeadsetReport(buf[:n])
		if err != nil {
			l.skipped++
			continue
		}
		if pkt.EventID == event && pkt.Address == wantFrom && match(pkt) {
			return pkt, nil
		}
		l.skipped++
	}
}

// ParseHeadsetReport parses one input report in the headset framing, [02][len][HCI event]...,
// ignoring anything after the length byte. It returns an error for reports that are not HCI
// events (for example the 3-byte report-ID-0x80 reports) or that fail the checksum.
func ParseHeadsetReport(report []byte) (*HciPacket, error) {
	if len(report) < 2 || report[0] != linkReportID {
		return nil, fmt.Errorf("not a headset hci report")
	}
	length := int(report[1])
	if length > len(report)-2 {
		length = len(report) - 2
	}
	return ParseEventExact(report[2 : 2+length])
}

// ParseEventExact parses an HCI event whose extent is given by its own length field:
//
//	04 ff PLEN 00 96 c3 ADDR EVT TYPE TX_LO TX_HI PARAM... CKS
//
// with PLEN = 9 + len(PARAM) (it counts the extra 00 byte and the checksum). Unlike
// ParseHciEvent it does not depend on the buffer having been trimmed beforehand, and it
// verifies the checksum.
func ParseEventExact(data []byte) (*HciPacket, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("event too short")
	}
	if data[0] != HciEvtPacketType || data[1] != 0xFF {
		return nil, fmt.Errorf("not a valid hci event")
	}
	plen := int(data[2])
	if plen < 9 {
		return nil, fmt.Errorf("event length %d too small", plen)
	}
	if 3+plen > len(data) {
		return nil, fmt.Errorf("event truncated: length says %d, have %d", 3+plen, len(data))
	}
	paramLen := plen - 9
	p := &HciPacket{
		PacketType: data[0],
		EventCode:  data[1],
		ParamLen:   data[2],
		SonyKeyID:  binary.LittleEndian.Uint16(data[4:6]),
		Address:    data[6],
		EventID:    data[7],
		EventType:  data[8],
		TxID:       binary.LittleEndian.Uint16(data[9:11]),
		Checksum:   data[11+paramLen],
	}
	if paramLen > 0 {
		p.Param = make([]byte, paramLen)
		copy(p.Param, data[11:11+paramLen])
	}
	if want := checksumCmd(p); p.Checksum != want {
		return nil, fmt.Errorf("bad checksum: got 0x%02X, want 0x%02X", p.Checksum, want)
	}
	return p, nil
}
