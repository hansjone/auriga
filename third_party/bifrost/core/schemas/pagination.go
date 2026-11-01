package schemas

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

type SerialCursor struct {
	Version  int    `json:"v"`
	KeyIndex int    `json:"i"`
	Cursor   string `json:"c"`
}

func EncodeSerialCursor(cursor *SerialCursor) string {
	if cursor == nil {
		return ""
	}
	data, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.URLEncoding.EncodeToString(data)
}

func DecodeSerialCursor(encoded string) (*SerialCursor, error) {
	if encoded == "" {
		return nil, nil
	}

	data, err := base64.URLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("failed to decode cursor: %w", err)
	}

	var cursor SerialCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cursor: %w", err)
	}

	if cursor.Version != 1 {
		return nil, fmt.Errorf("unsupported cursor version: %d", cursor.Version)
	}

	return &cursor, nil
}

func NewSerialCursor(keyIndex int, cursor string) *SerialCursor {
	return &SerialCursor{
		Version:  1,
		KeyIndex: keyIndex,
		Cursor:   cursor,
	}
}
