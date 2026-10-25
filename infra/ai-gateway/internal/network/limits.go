package network

// Hard limits from contracts/network/limits.json (must stay in sync with the contract).
const (
	AppVersion              = "1.0"
	FetchMaxItems           = 128
	FetchMaxBytes           = 65536
	FetchMinBytes           = 4096
	ReceiptMaxResults       = 256
	EncodingMaxPayloadBytes = 65536
)
