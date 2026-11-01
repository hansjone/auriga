package utils

import schemas "github.com/maximhq/bifrost/core/schemas"

type SerialListHelper struct {
	Keys   []schemas.Key
	Cursor *schemas.SerialCursor
	Logger schemas.Logger
}

func NewSerialListHelper(keys []schemas.Key, encodedCursor *string, logger schemas.Logger, allowNativeCursorFallback bool) (*SerialListHelper, error) {
	helper := &SerialListHelper{
		Keys:   keys,
		Logger: logger,
	}

	if encodedCursor != nil && *encodedCursor != "" {
		cursor, err := schemas.DecodeSerialCursor(*encodedCursor)
		if err != nil {
			if allowNativeCursorFallback && len(keys) == 1 {

				helper.Cursor = schemas.NewSerialCursor(0, *encodedCursor)
			} else {
				return nil, err
			}
		} else {
			helper.Cursor = cursor
		}
	}

	return helper, nil
}

func (h *SerialListHelper) GetCurrentKey() (schemas.Key, string, bool) {
	if len(h.Keys) == 0 {
		return schemas.Key{}, "", false
	}

	keyIndex := 0
	nativeCursor := ""

	if h.Cursor != nil {
		keyIndex = h.Cursor.KeyIndex
		nativeCursor = h.Cursor.Cursor
	}

	if keyIndex >= len(h.Keys) {
		return schemas.Key{}, "", false
	}

	return h.Keys[keyIndex], nativeCursor, true
}

func (h *SerialListHelper) BuildNextCursor(hasMore bool, nativeCursor string) (string, bool) {
	if len(h.Keys) == 0 {
		return "", false
	}

	currentKeyIndex := 0
	if h.Cursor != nil {
		currentKeyIndex = h.Cursor.KeyIndex
	}

	if hasMore {

		nextCursor := schemas.NewSerialCursor(currentKeyIndex, nativeCursor)
		return schemas.EncodeSerialCursor(nextCursor), true
	}

	nextKeyIndex := currentKeyIndex + 1
	if nextKeyIndex >= len(h.Keys) {

		return "", false
	}

	nextCursor := schemas.NewSerialCursor(nextKeyIndex, "")
	return schemas.EncodeSerialCursor(nextCursor), true
}
