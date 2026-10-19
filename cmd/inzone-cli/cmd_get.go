package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/patyhank/linzone-hub/internal/protocol"
	"github.com/patyhank/linzone-hub/internal/usb"
	"github.com/spf13/cobra"
)

// getEventNames maps friendly names to HCI event IDs for the `get` command.
var getEventNames = map[string]byte{
	"volume":       protocol.EvtHeadphoneVolume,
	"mix":          protocol.EvtGameChatMix,
	"sidetone":     protocol.EvtSidetoneVolume,
	"mic":          protocol.EvtMicVolume,
	"surround":     protocol.EvtSurroundSetting,
	"ambient":      protocol.EvtAmbSetting,
	"nc":           protocol.EvtNCToggle,
	"ncstartup":    protocol.EvtNCStartupMode,
	"btstatus":     protocol.EvtBTStatus,
	"btquality":    protocol.EvtBTSoundQuality,
	"btstartup":    protocol.EvtBTStartupMode,
	"autopoweroff": protocol.EvtAutoPowerOff,
	"battery":      protocol.EvtBatteryInfo,
}

func resolveEventID(arg string) (byte, error) {
	if id, ok := getEventNames[strings.ToLower(arg)]; ok {
		return id, nil
	}
	n, err := strconv.ParseUint(arg, 0, 8)
	if err != nil {
		return 0, fmt.Errorf("unknown event %q: use a name (%s) or a numeric event ID", arg, getEventNameList())
	}
	return byte(n), nil
}

func getEventNameList() string {
	names := make([]string, 0, len(getEventNames))
	for name := range getEventNames {
		names = append(names, name)
	}
	// stable, readable order
	order := []string{"volume", "mix", "sidetone", "mic", "surround", "ambient", "nc", "ncstartup", "btstatus", "btquality", "btstartup", "autopoweroff", "battery"}
	out := make([]string, 0, len(names))
	for _, n := range order {
		if _, ok := getEventNames[n]; ok {
			out = append(out, n)
		}
	}
	return strings.Join(out, ", ")
}

// queryHeadsetEvent opens the headset's control interface (the same way the GUI does)
// and sends a single GET for eventID. It never sends a SET.
func queryHeadsetEvent(idx int, eventID byte) (*protocol.HciPacket, protocol.HciFraming, usb.DeviceInfo, error) {
	devs, err := usb.Enumerate()
	if err != nil {
		return nil, 0, usb.DeviceInfo{}, err
	}
	if idx < 0 || idx >= len(devs) {
		return nil, 0, usb.DeviceInfo{}, fmt.Errorf("device index %d out of range (have %d devices)", idx, len(devs))
	}
	base := devs[idx]
	if !usb.IsHeadset(base.ProductID) {
		return nil, 0, base, fmt.Errorf("%s is not a headset device", base.Model)
	}
	candidates, err := usb.FindHeadsetControlInterfaces(base)
	if err != nil {
		return nil, 0, base, err
	}
	var lastErr error
	tried := map[string]bool{}
	for _, candidate := range candidates {
		// A headset's several HID collections share one device node; retrying the same path
		// would only repeat the same timeouts.
		if tried[candidate.Path] {
			continue
		}
		tried[candidate.Path] = true
		dev, err := usb.Open(candidate)
		if err != nil {
			lastErr = fmt.Errorf("open %s: %w", candidate.Path, err)
			continue
		}
		pkt, framing, err := protocol.GetEventAuto(dev, eventID)
		_ = dev.Close()
		if err == nil {
			return pkt, framing, candidate, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no control interface found")
	}
	return nil, 0, base, lastErr
}

// eventChecksum is the packet checksum rule: the byte sum of key ID, address, event ID,
// type, TxID and parameters.
func eventChecksum(pkt *protocol.HciPacket, param []byte) byte {
	sum := int(pkt.SonyKeyID&0xFF) + int(pkt.SonyKeyID>>8) + int(pkt.Address) + int(pkt.EventID) +
		int(pkt.EventType) + int(pkt.TxID&0xFF) + int(pkt.TxID>>8)
	for _, b := range param {
		sum += int(b)
	}
	return byte(sum & 0xFF)
}

// describeEvent renders a reply. Depending on the framing, the parser either reports the
// checksum separately (pkt.Checksum) or leaves it as the last byte of Param. We verify
// both shapes so the printed parameters never include the checksum when it can be told apart.
func describeEvent(pkt *protocol.HciPacket) string {
	param := pkt.Param
	checksum := "unverified"
	n := len(param)
	switch {
	case eventChecksum(pkt, param) == pkt.Checksum:
		checksum = fmt.Sprintf("0x%02X (valid)", pkt.Checksum)
	case n > 0 && eventChecksum(pkt, param[:n-1]) == param[n-1]:
		checksum = fmt.Sprintf("0x%02X (valid, was trailing byte of param)", param[n-1])
		param = param[:n-1]
	default:
		checksum = "does not match; showing all bytes as received"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "event=%d type=0x%02X addr=0x%02X txid=%d checksum: %s\n", pkt.EventID, pkt.EventType, pkt.Address, pkt.TxID, checksum)
	fmt.Fprintf(&b, "  param (%d bytes): % X   decimal: %v", len(param), param, []byte(param))
	switch pkt.EventID {
	case protocol.EvtHeadphoneVolume, protocol.EvtMicVolume:
		if len(param) >= 3 {
			fmt.Fprintf(&b, "\n  => mute=%d  byte1(value)=%d  byte2(percent)=%d", param[0], param[1], param[2])
		}
	case protocol.EvtAmbSetting:
		if len(param) >= 4 {
			fmt.Fprintf(&b, "\n  => mode=%d  level(raw)=%d  level(percent)=%d  voice_focus=%d", param[0], param[1], param[2], param[3])
		}
	}
	return b.String()
}

func init() {
	var watch bool
	var intervalMs int

	cmd := &cobra.Command{
		Use:   "get <device-index> <event>",
		Short: "Read-only: query one headset setting and print the raw reply",
		Long: `Sends a single GET for one setting and prints exactly what the headset replies.
It never sends a SET, so it cannot change any setting.

<event> is a name or a numeric event ID. Names: ` + getEventNameList() + `

Use --watch to poll repeatedly and print only when the reply changes (Ctrl-C to stop).
This is handy for seeing what the headset stores while you turn the volume wheel or
move a slider in the GUI.

Examples:
  go run ./cmd/inzone-cli get 0 volume
  go run ./cmd/inzone-cli get 0 volume --watch
  go run ./cmd/inzone-cli get 0 97`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			idx, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid device index %q", args[0])
			}
			eventID, err := resolveEventID(args[1])
			if err != nil {
				return err
			}
			if intervalMs < 100 {
				intervalMs = 100
			}

			pkt, framing, used, err := queryHeadsetEvent(idx, eventID)
			if err != nil {
				return err
			}
			fmt.Printf("%s  %s  usage_page=0x%04X  framing=%s\n", used.Model, used.Path, used.UsagePage, framing)
			text := describeEvent(pkt)
			fmt.Println(text)
			if !watch {
				return nil
			}

			fmt.Println("\nWatching (Ctrl-C to stop); printing on change...")
			start := time.Now()
			last := text
			for {
				time.Sleep(time.Duration(intervalMs) * time.Millisecond)
				pkt, _, _, err := queryHeadsetEvent(idx, eventID)
				if err != nil {
					fmt.Printf("[+%6.1fs] read error: %v\n", time.Since(start).Seconds(), err)
					continue
				}
				if cur := describeEvent(pkt); cur != last {
					fmt.Printf("[+%6.1fs] %s\n", time.Since(start).Seconds(), cur)
					last = cur
				}
			}
		},
	}
	cmd.Flags().BoolVar(&watch, "watch", false, "poll repeatedly and print only when the reply changes")
	cmd.Flags().IntVar(&intervalMs, "interval", 500, "poll interval in milliseconds for --watch (min 100)")
	rootCmd.AddCommand(cmd)
}
