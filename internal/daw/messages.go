package daw

import (
	"encoding/json"
	"fmt"
)

func queryUserLicenseBody() []byte {
	return []byte(
		`{"id": "QueryUserLicense","clientInternalName": "dawremote"}`,
	)
}

func subscribeBody(clientIdentifier string) []byte {
	identifier, err := json.Marshal(clientIdentifier)
	if err != nil {
		identifier = []byte(`"00000000-0000-4000-8000-000000000000"`)
	}
	return []byte(fmt.Sprintf(
		`{"id": "Subscribe","clientName": "Studio Pro Remote",`+
			`"clientInternalName": "dawremote","clientType": "iPad",`+
			`"clientDescription": "iPad","clientIdentifier": %s,`+
			`"clientOptions": "surround","clientEncoding": 23117}`,
		identifier,
	))
}

func mainConnID(endpoint Endpoint) [4]byte {
	return [4]byte{0x68, 0, endpoint.ServerID, 0}
}

func meterConnID(endpoint Endpoint) [4]byte {
	return [4]byte{0, 0, endpoint.ServerID, 0}
}
