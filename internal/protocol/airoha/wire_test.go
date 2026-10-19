package airoha

import (
	"bytes"
	"testing"

	"github.com/patyhank/linzone-hub/internal/protocol"
)

// The H9 II is driven through the Buds control methods on the premise that the Buds and
// headset SET frames are the same on the wire. This pins that premise: apart from the
// report ID and length byte the Buds builder adds, the HCI packets must be identical.
func TestBudsAndHeadsetSetFramesMatch(t *testing.T) {
	events := []byte{protocol.EvtHeadphoneVolume, protocol.EvtMicVolume, protocol.EvtSidetoneVolume, protocol.EvtGameChatMix, protocol.EvtAmbSetting, protocol.EvtSurroundSetting}
	params := [][]byte{{0, 15, 15}, {40, 40}, {1, 10, 10, 1}, {50}, {0}}
	for _, ev := range events {
		for _, param := range params {
			buds := BuildBudsHciSet(ev, 0, param)
			if buds == nil {
				t.Fatalf("event %d: Buds frame is nil", ev)
			}
			n := int(buds[1])
			if got, want := buds[2:2+n], protocol.BuildHciSet(ev, param); !bytes.Equal(got, want) {
				t.Errorf("event %d param %v:\n buds    % X\n headset % X", ev, param, got, want)
			}
		}
	}
}
