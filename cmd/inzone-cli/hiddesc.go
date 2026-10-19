package main

import (
	"fmt"
	"strings"
)

// summarizeReportDescriptor walks a raw HID report descriptor and returns one
// human-readable line per collection / report field. It only needs to be good
// enough to answer "which report IDs exist, in which direction, and how big".
func summarizeReportDescriptor(desc []byte) []string {
	var (
		out         []string
		usagePage   uint32
		reportSize  uint32
		reportCount uint32
		reportID    uint32
		usage       uint32
		haveUsage   bool
		depth       int
	)

	collectionNames := map[uint32]string{0: "Physical", 1: "Application", 2: "Logical", 3: "Report", 4: "Named Array", 5: "Usage Switch", 6: "Usage Modifier"}

	for i := 0; i < len(desc); {
		prefix := desc[i]
		if prefix == 0xFE { // long item: [0xFE][data length][long tag][data...]
			if i+1 >= len(desc) {
				break
			}
			i += 3 + int(desc[i+1])
			continue
		}

		size := int(prefix & 0x03)
		if size == 3 {
			size = 4
		}
		if i+1+size > len(desc) {
			out = append(out, "(descriptor truncated)")
			break
		}
		var val uint32
		for b := 0; b < size; b++ {
			val |= uint32(desc[i+1+b]) << (8 * uint(b))
		}
		itemType := (prefix >> 2) & 0x03
		tag := prefix >> 4
		indent := strings.Repeat("  ", depth)

		switch itemType {
		case 0: // main items
			switch tag {
			case 0x08, 0x09, 0x0B: // Input, Output, Feature
				kind := map[byte]string{0x08: "Input  ", 0x09: "Output ", 0x0B: "Feature"}[tag]
				id := "(no report id)"
				if reportID != 0 {
					id = fmt.Sprintf("report_id=0x%02X", reportID)
				}
				note := ""
				if val&0x01 != 0 {
					note = "  [constant/padding]"
				}
				out = append(out, fmt.Sprintf("%s%s %s  %d bit x %d = %d byte(s)%s",
					indent, kind, id, reportSize, reportCount, (reportSize*reportCount+7)/8, note))
			case 0x0A: // Collection
				name := collectionNames[val]
				if name == "" {
					name = fmt.Sprintf("0x%02X", val)
				}
				u := "usage=?"
				if haveUsage {
					u = fmt.Sprintf("usage=0x%04X", usage)
				}
				out = append(out, fmt.Sprintf("%sCollection(%s)  usage_page=0x%04X %s", indent, name, usagePage, u))
				depth++
			case 0x0C: // End Collection
				if depth > 0 {
					depth--
				}
			}
			// Local items are cleared after every main item.
			haveUsage = false
			usage = 0
		case 1: // global items
			switch tag {
			case 0x0:
				usagePage = val
			case 0x7:
				reportSize = val
			case 0x8:
				reportID = val
			case 0x9:
				reportCount = val
			}
		case 2: // local items
			if tag == 0x0 && !haveUsage {
				usage, haveUsage = val, true
			}
		}
		i += 1 + size
	}
	return out
}
