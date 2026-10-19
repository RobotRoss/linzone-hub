package app

// INZONE H9 II headphone volume.
//
// Verified on hardware: the headset stores whatever value it is sent in the "value" byte
// of HEADPHONE_VOLUME (event 33) and ignores the "percent" byte (it always reads back
// 0xFF), but its volume wheel only ever moves that value in single steps between 0 and
// 30 and never above 30. We therefore treat 0-30 as the headset's real range, rather
// than the 0-255 scale used for the older H9/H7, so that the GUI slider (0-100%) and the
// wheel agree and the slider never sends a value the wheel cannot reach.
const h9IIVolumeMax = 30

// h9IIVolumeFromPercent converts a 0-100 slider value to the headset's 0-30 step.
func h9IIVolumeFromPercent(percent int) byte {
	if percent <= 0 {
		return 0
	}
	if percent >= 100 {
		return h9IIVolumeMax
	}
	return byte((percent*h9IIVolumeMax + 50) / 100)
}

// h9IIPercentFromVolume converts the headset's 0-30 step to a 0-100 percentage.
// The headset's own percent byte is unusable (always 0xFF), so it is derived here.
// Values above 30 (the headset accepts them but the wheel cannot produce them) read as 100.
func h9IIPercentFromVolume(raw int) int {
	if raw <= 0 {
		return 0
	}
	if raw >= h9IIVolumeMax {
		return 100
	}
	return (raw*100 + h9IIVolumeMax/2) / h9IIVolumeMax
}
