package main

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	hid "github.com/sstallion/go-hid"

	"github.com/patyhank/linzone-hub/internal/protocol"
	"github.com/patyhank/linzone-hub/internal/usb"
)

// burstEvent is one query the GUI issues on a full refresh of an H9 II.
type burstEvent struct {
	name string
	id   byte
}

var burstEvents = []burstEvent{
	{"2ghz-status", protocol.Evt2GHZConnectStatus},
	{"model", protocol.EvtModelInfo},
	{"firmware", protocol.EvtFWVersion},
	{"battery", protocol.EvtBatteryInfo},
	{"volume", protocol.EvtHeadphoneVolume},
	{"mic", protocol.EvtMicVolume},
	{"ambient", protocol.EvtAmbSetting},
	{"mix", protocol.EvtGameChatMix},
	{"sidetone", protocol.EvtSidetoneVolume},
	{"surround", protocol.EvtSurroundSetting},
	{"btstatus", protocol.EvtBTStatus},
	{"btquality", protocol.EvtBTSoundQuality},
	{"btstartup", protocol.EvtBTStartupMode},
	{"autopoweroff", protocol.EvtAutoPowerOff},
	{"ncstartup", protocol.EvtNCStartupMode},
	{"conndest", protocol.EvtConnectionDest},
	{"assignable", protocol.EvtAssignableParam},
}

var errLinkTimeout = errors.New("timeout")

// hciLink is the minimal transport the burst test needs: GET queries in the headset
// framing only. It exists so the matching logic can be tested against a fake headset.
type hciLink interface {
	SendGet(eventID byte) error
	// Recv waits up to timeout. It returns errLinkTimeout on timeout, and (nil, nil) for a
	// report that is not an HCI event (for example a media-key report on the same node).
	Recv(timeout time.Duration) (*protocol.HciPacket, error)
}

// headsetPayload extracts the HCI bytes from a headset-framed report:
// [report ID 2][length][HCI packet...][padding].
func headsetPayload(report []byte) ([]byte, error) {
	if len(report) < 2 || report[0] != 0x02 {
		return nil, fmt.Errorf("not a headset HCI report")
	}
	length := int(report[1])
	if length > len(report)-2 {
		length = len(report) - 2
	}
	return report[2 : 2+length], nil
}

type hidLink struct{ dev *hid.Device }

func (l hidLink) SendGet(eventID byte) error {
	return protocol.SendHciReportWithFraming(l.dev, protocol.BuildHciGet(eventID, nil), protocol.HciFramingHeadset)
}

func (l hidLink) Recv(timeout time.Duration) (*protocol.HciPacket, error) {
	buf := make([]byte, 64)
	n, err := protocol.ReadReport(l.dev, buf, timeout)
	if err != nil {
		if errors.Is(err, hid.ErrTimeout) {
			return nil, errLinkTimeout
		}
		return nil, err
	}
	raw, err := headsetPayload(buf[:n])
	if err != nil {
		return nil, nil
	}
	pkt, err := protocol.ParseHciEvent(raw)
	if err != nil {
		return nil, nil
	}
	return pkt, nil
}

type observation struct {
	at    time.Duration // since the query (sequential) or since the first write (burst)
	typ   byte          // event type: 0x10 = reply to our GET, 0xA0 = unsolicited notification
	param []byte
}

func nameOf(id byte) string {
	for _, e := range burstEvents {
		if e.id == id {
			return e.name
		}
	}
	return strconv.Itoa(int(id))
}

// drainLink discards anything already queued so stale notifications do not skew a run.
func drainLink(link hciLink) {
	for i := 0; i < 64; i++ {
		if _, err := link.Recv(5 * time.Millisecond); errors.Is(err, errLinkTimeout) {
			return
		} else if err != nil {
			return
		}
	}
}

// runSequential asks one query at a time and waits up to perEvent for the matching reply.
func runSequential(link hciLink, events []burstEvent, perEvent time.Duration) (map[byte]observation, int, time.Duration, error) {
	obs := map[byte]observation{}
	other := 0
	start := time.Now()
	for _, ev := range events {
		t0 := time.Now()
		if err := link.SendGet(ev.id); err != nil {
			return obs, other, time.Since(start), err
		}
		deadline := t0.Add(perEvent)
		for {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				break
			}
			pkt, err := link.Recv(remaining)
			if errors.Is(err, errLinkTimeout) {
				break
			}
			if err != nil {
				return obs, other, time.Since(start), err
			}
			// A reply to our GET is type 0x10. An unsolicited notification (0xA0) can carry the same
			// event ID, so matching on the ID alone could mistake one for the other.
			if pkt == nil || pkt.EventID != ev.id || pkt.EventType != protocol.FlagRET {
				other++
				continue
			}
			obs[ev.id] = observation{at: time.Since(t0), typ: pkt.EventType, param: append([]byte(nil), pkt.Param...)}
			break
		}
	}
	return obs, other, time.Since(start), nil
}

type burstResult struct {
	obs       map[byte]observation
	missing   []byte        // expected replies that never arrived before the deadline
	other     int           // reports that were not a first reply to one of our queries
	sentIn    time.Duration // how long the writes took
	completed time.Duration // when the last expected reply arrived (0 if some are missing)
}

// runBurst writes every query back-to-back, then collects replies and matches them by
// event ID until all of `expect` have arrived or the deadline passes.
func runBurst(link hciLink, events []burstEvent, expect map[byte]bool, gap, deadlineAfter time.Duration) (burstResult, error) {
	res := burstResult{obs: map[byte]observation{}}
	t0 := time.Now()
	for i, ev := range events {
		if err := link.SendGet(ev.id); err != nil {
			return res, err
		}
		if gap > 0 && i < len(events)-1 {
			time.Sleep(gap)
		}
	}
	res.sentIn = time.Since(t0)

	listed := map[byte]bool{}
	for _, ev := range events {
		listed[ev.id] = true
	}
	pending := len(expect)
	deadline := t0.Add(deadlineAfter)
	for pending > 0 {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		pkt, err := link.Recv(remaining)
		if errors.Is(err, errLinkTimeout) {
			break
		}
		if err != nil {
			return res, err
		}
		if pkt == nil || !listed[pkt.EventID] || pkt.EventType != protocol.FlagRET {
			res.other++
			continue
		}
		if _, dup := res.obs[pkt.EventID]; dup {
			res.other++
			continue
		}
		res.obs[pkt.EventID] = observation{at: time.Since(t0), typ: pkt.EventType, param: append([]byte(nil), pkt.Param...)}
		if expect[pkt.EventID] {
			pending--
			if pending == 0 {
				res.completed = time.Since(t0)
			}
		}
	}
	for _, ev := range events {
		if expect[ev.id] {
			if _, ok := res.obs[ev.id]; !ok {
				res.missing = append(res.missing, ev.id)
			}
		}
	}
	return res, nil
}

func medianDuration(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000) }

func init() {
	var perEventMs, burstMs, gapMs, repeat int

	cmd := &cobra.Command{
		Use:   "burst <device-index>",
		Short: "Read-only experiment: sequential queries vs. sending all queries then matching replies by event ID",
		Long: `Measures how an INZONE headset (built for the H9 II) handles the GUI's full-refresh queries two ways:

  1. sequential: one query, wait for its reply, next query   (what the app does today)
  2. burst:      write every query back-to-back, then collect replies and match them by event ID

It then compares the values from both, and repeats the burst several times to show whether
any replies are lost or mismatched when queries are sent without waiting. Only GET queries
are sent, so it cannot change any setting. Uses the headset framing only (no retry).

Examples:
  go run ./cmd/inzone-cli burst 0
  go run ./cmd/inzone-cli burst 0 --repeat 20 --gap 0
  go run ./cmd/inzone-cli burst 0 --gap 3        # pace the writes 3 ms apart`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			idx, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid device index %q", args[0])
			}
			devs, err := usb.Enumerate()
			if err != nil {
				return err
			}
			if idx < 0 || idx >= len(devs) {
				return fmt.Errorf("device index %d out of range (have %d devices)", idx, len(devs))
			}
			base := devs[idx]
			if !usb.IsHeadset(base.ProductID) {
				return fmt.Errorf("%s is not a headset device", base.Model)
			}
			dev, err := usb.Open(base)
			if err != nil {
				return fmt.Errorf("open %s: %w", base.Path, err)
			}
			defer dev.Close()
			link := hidLink{dev: dev}

			fmt.Printf("%s  %s  (headset framing only, GET queries only)\n", base.Model, base.Path)
			drainLink(link)

			fmt.Printf("\n== Sequential (per-query timeout %d ms) ==\n", perEventMs)
			seq, seqOther, seqTotal, err := runSequential(link, burstEvents, time.Duration(perEventMs)*time.Millisecond)
			if err != nil {
				return err
			}
			expect := map[byte]bool{}
			for _, ev := range burstEvents {
				o, ok := seq[ev.id]
				if ok {
					expect[ev.id] = true
					fmt.Printf("  %-13s id=%-3d reply in %-9s type=0x%02X\n", ev.name, ev.id, ms(o.at), o.typ)
				} else {
					fmt.Printf("  %-13s id=%-3d NO REPLY\n", ev.name, ev.id)
				}
			}
			fmt.Printf("  total %s, %d answered, %d silent, %d unrelated reports\n", ms(seqTotal), len(expect), len(burstEvents)-len(expect), seqOther)
			if len(expect) == 0 {
				return fmt.Errorf("the headset answered none of the queries; is the transmitter in PC mode?")
			}

			fmt.Printf("\n== Burst x%d (gap %d ms, deadline %d ms) ==\n", repeat, gapMs, burstMs)
			var times []time.Duration
			complete, mismatchRuns := 0, 0
			for r := 1; r <= repeat; r++ {
				drainLink(link)
				res, err := runBurst(link, burstEvents, expect, time.Duration(gapMs)*time.Millisecond, time.Duration(burstMs)*time.Millisecond)
				if err != nil {
					return err
				}
				var mism []string
				for _, ev := range burstEvents {
					if !expect[ev.id] {
						continue
					}
					if o, ok := res.obs[ev.id]; ok && !bytes.Equal(o.param, seq[ev.id].param) {
						mism = append(mism, ev.name)
					}
				}
				status := fmt.Sprintf("%d/%d replies in %s (writes took %s, %d extra)", len(expect)-len(res.missing), len(expect), ms(res.completed), ms(res.sentIn), res.other)
				if len(res.missing) > 0 {
					names := make([]string, 0, len(res.missing))
					for _, id := range res.missing {
						names = append(names, nameOf(id))
					}
					status = fmt.Sprintf("LOST %v (%d/%d replies; waited the full %d ms)", names, len(expect)-len(res.missing), len(expect), burstMs)
				} else {
					complete++
					times = append(times, res.completed)
				}
				if len(mism) > 0 {
					mismatchRuns++
					status += fmt.Sprintf("  values differ from sequential: %v", mism)
				}
				fmt.Printf("  run %2d: %s\n", r, status)
			}
			fmt.Printf("\n== Summary ==\n")
			fmt.Printf("  sequential: %s for %d answered queries (plus %d silent ones that each cost the %d ms timeout)\n", ms(seqTotal), len(expect), len(burstEvents)-len(expect), perEventMs)
			if len(times) > 0 {
				sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
				fmt.Printf("  burst:      %d/%d runs got every reply; time to last reply min %s / median %s / max %s\n", complete, repeat, ms(times[0]), ms(medianDuration(times)), ms(times[len(times)-1]))
			} else {
				fmt.Printf("  burst:      0/%d runs got every reply; sending all queries without waiting does not work as-is\n", repeat)
			}
			fmt.Printf("  runs with values that differed from the sequential pass: %d (a moving wheel or button press can cause this legitimately)\n", mismatchRuns)
			return nil
		},
	}
	cmd.Flags().IntVar(&perEventMs, "timeout", 250, "sequential per-query timeout in milliseconds")
	cmd.Flags().IntVar(&burstMs, "burst-timeout", 600, "deadline for collecting burst replies in milliseconds")
	cmd.Flags().IntVar(&gapMs, "gap", 0, "pause between burst writes in milliseconds")
	cmd.Flags().IntVar(&repeat, "repeat", 10, "number of burst runs")
	rootCmd.AddCommand(cmd)
}
