package app

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/patyhank/linzone-hub/internal/protocol"
)

func TestStatePatchCarriesOnlyTheChangedSetting(t *testing.T) {
	cases := []struct {
		name  string
		event byte
		param []byte
		want  string // JSON of the expected patch
	}{
		{"volume wheel", 33, []byte{0x00, 0x1e, 0xff}, `{"headphoneVolume":{"mute":0,"raw":30,"percent":100}}`},
		{"boom removed", 143, []byte{0x01}, `{"micAttached":false}`},
		{"boom attached", 143, []byte{0x00}, `{"micAttached":true}`},
		{"mic mute button", 36, []byte{0x01, 0xff, 0xff}, `{"micVolume":{"mute":1,"raw":255,"percent":0}}`},
		{"battery drift", 4, []byte{0x00, 0x5c}, `{"battery":{"headset":{"percent":92,"status":0}}}`},
		{"ANC button", 65, []byte{0x02, 0x0a, 0xff, 0x01}, `{"ambient":{"mode":2,"ambientRaw":10,"ambientPercent":47,"voiceFocus":true}}`},
		{"bluetooth connects", 97, []byte{0x01, 0x01}, `{"btStatus":{"eventId":97,"value":1,"raw":[1,1]}}`},
		{"auto power off", 129, []byte{0x1e, 0x1e}, `{"autoPowerOff":{"eventId":129,"value":30,"raw":[30,30]}}`},
	}
	for _, c := range cases {
		got, err := h9iiStatePatch(c.event, c.param)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		var want map[string]any
		if err := json.Unmarshal([]byte(c.want), &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			gj, _ := json.Marshal(got)
			t.Errorf("%s:\n got  %s\n want %s", c.name, gj, c.want)
		}
	}
}

func TestStatePatchIgnoresEventsTheGUIDoesNotShow(t *testing.T) {
	for _, ev := range []byte{37, 66, 131, 132, 200} {
		if p, err := h9iiStatePatch(ev, []byte{1, 2, 3}); p != nil || err != nil {
			t.Errorf("event %d: patch=%v err=%v, want nothing", ev, p, err)
		}
	}
}

func TestStatePatchRejectsAGarbledPayload(t *testing.T) {
	if p, err := h9iiStatePatch(33, []byte{0x00}); err == nil || p != nil {
		t.Errorf("a short volume payload must be an error, got %v %v", p, err)
	}
}

func TestNotificationsReachTheEmitterWithTheDeviceIndex(t *testing.T) {
	type got struct {
		name    string
		payload any
	}
	var seen []got
	s := NewServiceWithEmitter(func(name string, payload any) { seen = append(seen, got{name, payload}) })

	s.handleH9IINotification(3, &protocol.HciPacket{EventID: 143, EventType: protocol.TypeNotify, TxID: 1, Param: []byte{0x01}})
	s.handleH9IINotification(3, &protocol.HciPacket{EventID: 200, EventType: protocol.TypeNotify, TxID: 1, Param: []byte{0x01}}) // not shown by the GUI
	s.handleH9IINotification(3, &protocol.HciPacket{EventID: 33, EventType: protocol.TypeNotify, TxID: 1, Param: []byte{0x00}})  // garbled

	if len(seen) != 1 || seen[0].name != EventStateChanged {
		t.Fatalf("want exactly one state event, got %+v", seen)
	}
	ev, ok := seen[0].payload.(StateEvent)
	if !ok || ev.Index != 3 || ev.Event != 143 || ev.State["micAttached"] != false {
		t.Errorf("payload: %+v", seen[0].payload)
	}

	// the payload must survive JSON, which is how Wails delivers it
	if raw, err := json.Marshal(ev); err != nil || string(raw) != `{"index":3,"event":143,"state":{"micAttached":false}}` {
		t.Errorf("json: %s %v", raw, err)
	}
}

func TestServiceWithoutAnEmitterIgnoresNotifications(t *testing.T) {
	NewService().handleH9IINotification(0, &protocol.HciPacket{EventID: 143, Param: []byte{0x01}}) // must not panic
}
