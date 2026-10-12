package daw

import (
	"bytes"
	"testing"

	"github.com/steveclarke/ucmix/internal/proto"
)

func TestRecorderRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	r := NewRecorder(&buf)
	in := []proto.Frame{
		{Code: proto.CodePV, ConnID: [4]byte{1, 2, 3, 4}, Payload: proto.MarshalPV("transport/start", 1)},
		jmFrame(`{"id": "SubscriptionReply"}`),
	}
	for _, f := range in {
		r.Tap(f)
	}
	out, err := ReadCapture(&buf)
	if err != nil || len(out) != 2 || r.Frames != 2 {
		t.Fatalf("ReadCapture = %d frames, %v", len(out), err)
	}
	if out[0].Code != proto.CodePV || out[0].ConnID != in[0].ConnID || !bytes.Equal(out[1].Payload, in[1].Payload) {
		t.Errorf("frames differ: %+v", out)
	}
}

func TestDialTapSeesHandshake(t *testing.T) {
	fake := newFake(t)
	var buf bytes.Buffer
	rec := NewRecorder(&buf)
	dialFakeTap(t, fake, rec.Tap)
	frames, _ := ReadCapture(&buf)
	var last proto.Frame
	if len(frames) > 0 {
		last = frames[len(frames)-1]
	}
	msg, _ := proto.ParseJM(last.Payload)
	if len(frames) < 3 || msg.ID != "SubscriptionReply" {
		t.Errorf("recorded %d frames, last %q", len(frames), msg.ID)
	}
}
