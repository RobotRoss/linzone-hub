package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	hid "github.com/sstallion/go-hid"

	"github.com/patyhank/linzone-hub/internal/protocol"
	"github.com/patyhank/linzone-hub/internal/usb"
	"github.com/spf13/cobra"
)

func init() {
	var pidFlag string
	var listenMs int

	cmd := &cobra.Command{
		Use:   "race-probe",
		Short: "Read-only probe of a vendor (UsagePage 0xFF13) HID collection",
		Long: `Finds a Sony HID collection on UsagePage 0xFF13 (the Airoha RACE page), then:

  1. prints its HID report descriptor, plus a decoded summary of report IDs and sizes
  2. passively listens for input reports and prints them as hex

This command NEVER writes to the device. Nothing is sent, so no settings can change.
It exists to find out what an unsupported device (e.g. INZONE H9 II, PID 0x0FA9)
actually exposes before any protocol support is written.

While the listen window is open, interact with the headset: press the mute button,
turn the volume wheel, change ANC/ambient mode, connect/disconnect the transceiver.

Examples:
  go run ./cmd/inzone-cli race-probe --pid 0x0FA9
  go run ./cmd/inzone-cli race-probe --pid 0x0FA9 --listen 15000`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var wantPID uint16
			if pidFlag != "" {
				v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(pidFlag), "0x"), 16, 16)
				if err != nil {
					return fmt.Errorf("invalid --pid %q (expected hex like 0x0FA9): %w", pidFlag, err)
				}
				wantPID = uint16(v)
			}
			if listenMs < 100 {
				listenMs = 100
			}
			if listenMs > 60000 {
				listenMs = 60000
			}

			all, err := usb.EnumerateAll()
			if err != nil {
				return err
			}
			var target *usb.DeviceInfo
			for i := range all {
				d := &all[i]
				if d.UsagePage != usb.UsagePageAirohaRace {
					continue
				}
				if pidFlag != "" && d.ProductID != wantPID {
					continue
				}
				target = d
				break
			}
			if target == nil {
				return fmt.Errorf("no Sony HID collection on UsagePage 0x%04X found (pid filter: %q); run `debug` to list interfaces", usb.UsagePageAirohaRace, pidFlag)
			}

			fmt.Printf("Target: %s  VID=0x%04X PID=0x%04X  Path=%s  Interface=%d\n",
				target.Model, target.VendorID, target.ProductID, target.Path, target.InterfaceNbr)

			dev, err := usb.Open(*target)
			if err != nil {
				return fmt.Errorf("%w\n\nIf this is a permission error, install the udev rules (`inzone-cli udev`), replug the device, or try sudo", err)
			}
			defer dev.Close()

			// 1. Report descriptor (read-only).
			fmt.Println("\n== HID report descriptor ==")
			desc := make([]byte, 4096)
			n, err := dev.GetReportDescriptor(desc)
			if err != nil {
				fmt.Printf("could not read report descriptor: %v\n", err)
			} else {
				desc = desc[:n]
				fmt.Printf("%d bytes\n", n)
				for off := 0; off < len(desc); off += 16 {
					end := off + 16
					if end > len(desc) {
						end = len(desc)
					}
					fmt.Printf("  %04X: % X\n", off, desc[off:end])
				}
				fmt.Println("\n== Decoded ==")
				for _, line := range summarizeReportDescriptor(desc) {
					fmt.Println("  " + line)
				}
			}

			// 2. Passive listen (read-only).
			fmt.Printf("\n== Listening passively for %d ms (nothing is sent) ==\n", listenMs)
			fmt.Println("Interact with the headset now: mute button, volume wheel, ANC/ambient, etc.")
			start := time.Now()
			deadline := start.Add(time.Duration(listenMs) * time.Millisecond)
			buf := make([]byte, 512)
			count := 0
			for {
				remaining := time.Until(deadline)
				if remaining <= 0 {
					break
				}
				n, err := protocol.ReadReport(dev, buf, remaining)
				if errors.Is(err, hid.ErrTimeout) {
					break
				}
				if err != nil {
					return fmt.Errorf("read: %w", err)
				}
				count++
				fmt.Printf("  [%02d] +%4dms  %3d bytes: % X\n", count, time.Since(start).Milliseconds(), n, buf[:n])
			}
			if count == 0 {
				fmt.Println("  No reports received. The device may only speak when queried (we deliberately didn't).")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&pidFlag, "pid", "", "only consider this product ID (hex, e.g. 0x0FA9)")
	cmd.Flags().IntVar(&listenMs, "listen", 5000, "passive listen duration in milliseconds (100-60000)")
	rootCmd.AddCommand(cmd)
}
