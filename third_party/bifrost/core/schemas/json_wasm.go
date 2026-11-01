package schemas

import (
	"bytes"
	"encoding/json"
)

func Marshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

func MarshalString(v interface{}) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func Unmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

func Compact(dst *bytes.Buffer, src []byte) error {
	return json.Compact(dst, src)
}

func MarshalSorted(v interface{}) ([]byte, error) {
	normalized := normalizeForSortedMarshal(v)
	return json.Marshal(normalized)
}

func normalizeForSortedMarshal(v interface{}) interface{} {
	if v == nil {
		return nil
	}

	switch val := v.(type) {
	case *OrderedMap:
		if val == nil {
			return nil
		}
		result := make(map[string]interface{}, val.Len())
		val.Range(func(k string, v interface{}) bool {
			result[k] = normalizeForSortedMarshal(v)
			return true
		})
		return result
	case OrderedMap:
		result := make(map[string]interface{}, val.Len())
		val.Range(func(k string, v interface{}) bool {
			result[k] = normalizeForSortedMarshal(v)
			return true
		})
		return result
	case map[string]interface{}:
		result := make(map[string]interface{}, len(val))
		for k, v := range val {
			result[k] = normalizeForSortedMarshal(v)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, elem := range val {
			result[i] = normalizeForSortedMarshal(elem)
		}
		return result
	default:
		return v
	}
}
