package app

import "testing"

func TestH9IIVolumeFromPercent(t *testing.T) {
	cases := map[int]byte{-5: 0, 0: 0, 1: 0, 10: 3, 33: 10, 50: 15, 90: 27, 100: 30, 150: 30}
	for in, want := range cases {
		if got := h9IIVolumeFromPercent(in); got != want {
			t.Errorf("h9IIVolumeFromPercent(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestH9IIPercentFromVolume(t *testing.T) {
	cases := map[int]int{-1: 0, 0: 0, 3: 10, 15: 50, 30: 100, 31: 100, 128: 100, 255: 100}
	for in, want := range cases {
		if got := h9IIPercentFromVolume(in); got != want {
			t.Errorf("h9IIPercentFromVolume(%d) = %d, want %d", in, got, want)
		}
	}
}

// The slider must never send a value the wheel cannot reach, must be monotonic, and a
// value read back from the headset must land close to where the slider was.
func TestH9IIVolumeRoundTrip(t *testing.T) {
	prev := byte(0)
	for pct := 0; pct <= 100; pct++ {
		raw := h9IIVolumeFromPercent(pct)
		if raw > h9IIVolumeMax {
			t.Fatalf("pct %d -> %d exceeds wheel maximum %d", pct, raw, h9IIVolumeMax)
		}
		if raw < prev {
			t.Fatalf("not monotonic at pct %d: %d < %d", pct, raw, prev)
		}
		prev = raw
		back := h9IIPercentFromVolume(int(raw))
		if diff := back - pct; diff < -2 || diff > 2 { // one step is 3.33%, so rounding error is at most ~1.7%
			t.Errorf("pct %d -> raw %d -> %d%% (off by %d)", pct, raw, back, diff)
		}
	}
	// Every wheel position must read back distinctly and in order.
	last := -1
	for raw := 0; raw <= h9IIVolumeMax; raw++ {
		p := h9IIPercentFromVolume(raw)
		if p <= last {
			t.Errorf("wheel step %d reads %d%%, not above previous %d%%", raw, p, last)
		}
		last = p
	}
}

// Real readings from the hardware log: the wheel's ceiling (30), and the values the previous
// 0-255 mapping wrote for the 10/30/50/80/100% slider positions (26/77/128/204/255). Anything
// above the wheel's range reads as 100%.
func TestH9IIObservedValues(t *testing.T) {
	cases := map[int]int{30: 100, 26: 87, 77: 100, 128: 100, 204: 100, 255: 100}
	for raw, want := range cases {
		if got := h9IIPercentFromVolume(raw); got != want {
			t.Errorf("raw %d read back as %d%%, want %d%%", raw, got, want)
		}
	}
}
