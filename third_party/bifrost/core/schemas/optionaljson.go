package schemas

import (
	"bytes"
	"encoding/json"
)

type OptionalJSON[T any] struct {
	Set   bool
	Null  bool
	Value T
}

func (o *OptionalJSON[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	o.Null = false
	var zero T
	o.Value = zero
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		o.Null = true
		return nil
	}
	return json.Unmarshal(data, &o.Value)
}

func (o OptionalJSON[T]) MarshalJSON() ([]byte, error) {
	if !o.Set || o.Null {
		return []byte("null"), nil
	}
	return Marshal(o.Value)
}

func (o OptionalJSON[T]) IsZero() bool {
	return !o.Set
}
