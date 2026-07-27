package daw

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/steveclarke/ucmix/internal/proto"
)

func TestConnectionIdentitiesUseAnnouncedServerID(t *testing.T) {
	endpoint := Endpoint{ServerID: 0x6b}
	if got := mainConnID(endpoint); got != [4]byte{0x68, 0, 0x6b, 0} {
		t.Fatalf("mainConnID = % x", got)
	}
	if got := meterConnID(endpoint); got != [4]byte{0, 0, 0x6b, 0} {
		t.Fatalf("meterConnID = % x", got)
	}
}

func TestQueryUserLicenseMessage(t *testing.T) {
	frame := proto.Frame{
		Code:    proto.CodeJM,
		ConnID:  [4]byte{0x68, 0, 0x6b, 0},
		Payload: proto.MarshalJM(queryUserLicense()),
	}
	msg, err := proto.ParseJM(frame.Payload)
	if err != nil || msg.ID != "QueryUserLicense" {
		t.Fatalf("ParseJM = %+v, %v", msg, err)
	}
	var body map[string]any
	if err := json.Unmarshal(msg.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["clientInternalName"] != "dawremote" {
		t.Fatalf("clientInternalName = %v", body["clientInternalName"])
	}
}

func TestSubscribeMatchesCapturedDAWFields(t *testing.T) {
	payload := proto.MarshalJM(subscribe("00000000-0000-4000-8000-000000000000"))
	msg, err := proto.ParseJM(payload)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"id":"Subscribe","clientName":"Studio Pro Remote","clientInternalName":"dawremote","clientType":"iPad","clientDescription":"iPad","clientIdentifier":"00000000-0000-4000-8000-000000000000","clientOptions":"surround","clientEncoding":23117}`)
	if !bytes.Equal(msg.Body, want) {
		t.Fatalf("Subscribe body = %s, want %s", msg.Body, want)
	}
}
