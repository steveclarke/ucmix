package daw

type queryLicenseMessage struct {
	ID                 string `json:"id"`
	ClientInternalName string `json:"clientInternalName"`
}

type subscribeMessage struct {
	ID                 string `json:"id"`
	ClientName         string `json:"clientName"`
	ClientInternalName string `json:"clientInternalName"`
	ClientType         string `json:"clientType"`
	ClientDescription  string `json:"clientDescription"`
	ClientIdentifier   string `json:"clientIdentifier"`
	ClientOptions      string `json:"clientOptions"`
	ClientEncoding     int    `json:"clientEncoding"`
}

func mainConnID(endpoint Endpoint) [4]byte {
	return [4]byte{0x68, 0, endpoint.ServerID, 0}
}

func meterConnID(endpoint Endpoint) [4]byte {
	return [4]byte{0, 0, endpoint.ServerID, 0}
}

func queryUserLicense() queryLicenseMessage {
	return queryLicenseMessage{
		ID:                 "QueryUserLicense",
		ClientInternalName: "dawremote",
	}
}

func subscribe(identifier string) subscribeMessage {
	return subscribeMessage{
		ID:                 "Subscribe",
		ClientName:         "Studio Pro Remote",
		ClientInternalName: "dawremote",
		ClientType:         "iPad",
		ClientDescription:  "iPad",
		ClientIdentifier:   identifier,
		ClientOptions:      "surround",
		ClientEncoding:     23117,
	}
}
