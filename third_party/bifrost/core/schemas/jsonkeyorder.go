package schemas

import (
	"bytes"
	"encoding/json"
)

type JSONKeyOrder struct {
	keys []string
}

func (o *JSONKeyOrder) Capture(data []byte) {
	o.keys = ExtractTopLevelKeyOrder(data)
}

func (o *JSONKeyOrder) Apply(data []byte) ([]byte, error) {
	if len(o.keys) == 0 {
		return data, nil
	}
	return ReorderJSONKeys(data, o.keys)
}

func ExtractTopLevelKeyOrder(data []byte) []string {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))

	if _, err := dec.Token(); err != nil {
		return nil
	}

	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		key, ok := tok.(string)
		if !ok {
			break
		}
		keys = append(keys, key)

		if err := skipJSONValue(dec); err != nil {
			break
		}
	}
	return keys
}

func skipJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		for dec.More() {

			if _, err := dec.Token(); err != nil {
				return err
			}
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	}
	return nil
}

func ReorderJSONKeys(data []byte, order []string) ([]byte, error) {

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) < 2 || trimmed[0] != '{' {
		return data, nil
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	if _, err := dec.Token(); err != nil {
		return data, nil
	}

	type kvPair struct {
		key string
		val json.RawMessage
	}

	var pairs []kvPair
	pairMap := make(map[string]json.RawMessage)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return data, nil
		}
		key, ok := tok.(string)
		if !ok {
			return data, nil
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return data, nil
		}
		pairs = append(pairs, kvPair{key, val})
		pairMap[key] = val
	}

	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	emitted := make(map[string]bool, len(order))

	for _, key := range order {
		val, exists := pairMap[key]
		if !exists {
			continue
		}
		if !first {
			buf.WriteByte(',')
		}
		first = false
		keyBytes, _ := MarshalSorted(key)
		buf.Write(keyBytes)
		buf.WriteByte(':')
		buf.Write(val)
		emitted[key] = true
	}

	for _, kv := range pairs {
		if emitted[kv.key] {
			continue
		}
		if !first {
			buf.WriteByte(',')
		}
		first = false
		keyBytes, _ := MarshalSorted(kv.key)
		buf.Write(keyBytes)
		buf.WriteByte(':')
		buf.Write(kv.val)
	}

	buf.WriteByte('}')
	return buf.Bytes(), nil
}
