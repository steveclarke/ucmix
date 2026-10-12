package daw

import (
	"bytes"
	"encoding/hex"
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
		Payload: proto.MarshalJMRaw(queryUserLicenseBody()),
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

func TestQueryUserLicenseMatchesCapturedFrame(t *testing.T) {
	got := proto.Encode(proto.Frame{
		Code:    proto.CodeJM,
		ConnID:  [4]byte{0x68, 0, 0x6b, 0},
		Payload: proto.MarshalJMRaw(queryUserLicenseBody()),
	})
	want, err := hex.DecodeString(
		"5543000146004a4d68006b003c000000" +
			"7b226964223a20225175657279557365724c6963656e7365222c" +
			"22636c69656e74496e7465726e616c4e616d65223a2022646177" +
			"72656d6f7465227d",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("QueryUserLicense frame = % x, want % x", got, want)
	}
}

func TestSubscribeMatchesCapturedDAWFields(t *testing.T) {
	payload := proto.MarshalJMRaw(
		subscribeBody("00000000-0000-4000-8000-000000000000"),
	)
	msg, err := proto.ParseJM(payload)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"id": "Subscribe","clientName": "Studio Pro Remote","clientInternalName": "dawremote","clientType": "iPad","clientDescription": "iPad","clientIdentifier": "00000000-0000-4000-8000-000000000000","clientOptions": "surround","clientEncoding": 23117}`)
	if !bytes.Equal(msg.Body, want) {
		t.Fatalf("Subscribe body = %s, want %s", msg.Body, want)
	}
}
