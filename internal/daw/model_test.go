package daw

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"testing"

	"github.com/steveclarke/ucmix/internal/proto"
)

const syntheticSync = `{"id": "Synchronize","ranges": {"_channelColors": [1,2]},` +
	`"values": {"_channelColors": 4278190080},"children": {` +
	`"transport": {"values": {"start": 0.0,"stop": 1.0,"tempo": 0.28205129504203796}},` +
	`"metronome": {"values": {"clickOn": 1.0,"precountDisplay": ""}},` +
	`"mixer": {"children": {"channel": {"values": {"count": 0.002},"children": {` +
	`"ch1": {"values": {"label": "Guitar","type": "AudioTrack"},"children": {"inserts": {"values": {"count": 0.001}}}}}}}}}}`

func zm(t *testing.T, body string) proto.Frame {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	_, _ = w.Write([]byte(body))
	_ = w.Close()
	payload := make([]byte, 4, 4+buf.Len())
	binary.LittleEndian.PutUint32(payload, uint32(len(body)))
	return proto.Frame{Code: codeZM, Payload: append(payload, buf.Bytes()...)}
}

func TestModelAppliesSynchronize(t *testing.T) {
	m := NewModel()
	if err := m.ApplyFrame(zm(t, syntheticSync)); err != nil {
		t.Fatal(err)
	}
	if v, ok := m.Num("metronome/clickOn"); !ok || v != 1 {
		t.Errorf("clickOn = %v, %v", v, ok)
	}
	if v, _ := m.Num("mixer/channel/count"); v != 0.002 {
		t.Errorf("count = %v", v)
	}
	if s, _ := m.Str("mixer/channel/ch1/label"); s != "Guitar" {
		t.Errorf("label = %q", s)
	}
	if v, _ := m.Num("mixer/channel/ch1/inserts/count"); v != 0.001 {
		t.Errorf("inserts/count = %v", v)
	}
	if _, ok := m.Num("_channelColors"); ok {
		t.Error("underscore values should be skipped")
	}
}

func TestModelAppliesParameterFrames(t *testing.T) {
	m := NewModel()
	before := m.Seq()
	wake := m.Changed()
	frames := []proto.Frame{
		{Code: proto.CodePV, Payload: proto.MarshalPV("transport/start", 1)},
		{Code: proto.CodePS, Payload: proto.MarshalPS("mixer/channel/ch1/label", "Track 1")},
		{Code: proto.CodePC, Payload: proto.MarshalPC("mixer/channel/ch1/color", []byte{0xff, 0x2a, 0x2a, 0xff})},
	}
	for _, f := range frames {
		if err := m.ApplyFrame(f); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-wake:
	default:
		t.Error("Changed channel not closed")
	}
	if v, _ := m.Num("transport/start"); v != 1 {
		t.Errorf("start = %v", v)
	}
	if s, _ := m.Str("mixer/channel/ch1/label"); s != "Track 1" {
		t.Errorf("label = %q", s)
	}
	if v, _ := m.Num("mixer/channel/ch1/color"); v != 0xff2a2aff {
		t.Errorf("color = %x", uint32(v))
	}
	got := m.ChangesSince(before)
	if len(got) != 3 || got[0] != "transport/start" {
		t.Errorf("ChangesSince = %v", got)
	}
}

func TestModelLoadsAdvisedCommands(t *testing.T) {
	m := NewModel()
	body := `{"id": "AdviseCommands","categories": [{"displayName": "Transport","commands": [` +
		`{"displayName": "Click","name": "Click","category": "Transport","englishName": "Click"}]},` +
		`{"displayName": "Track","commands": [{"displayName": "Add Audio Track (mono)",` +
		`"name": "Add Audio Track (mono)","category": "Track","englishName": "Add Audio Track (mono)"}]}]}`
	if err := m.ApplyJSON([]byte(body)); err != nil {
		t.Fatal(err)
	}
	cmds := m.Commands()
	if len(cmds) != 2 || cmds[1] != (Command{Category: "Track", Name: "Add Audio Track (mono)", Display: "Add Audio Track (mono)"}) {
		t.Errorf("Commands = %+v", cmds)
	}
}

func TestDecodeZMRejectsShortPayload(t *testing.T) {
	if _, err := DecodeZM([]byte{1, 2}); err == nil {
		t.Error("want error")
	}
}
