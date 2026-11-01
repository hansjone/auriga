package schemas

import (
	"bytes"
	"encoding/json"
	"reflect"

	"github.com/bytedance/sonic"
)

func Marshal(v interface{}) ([]byte, error) {
	return sonic.Marshal(v)
}

func MarshalString(v interface{}) (string, error) {
	return sonic.MarshalString(v)
}

func Unmarshal(data []byte, v interface{}) error {
	return sonic.Unmarshal(data, v)
}

func Compact(dst *bytes.Buffer, src []byte) error {
	return json.Compact(dst, src)
}

func MarshalSorted(v interface{}) ([]byte, error) {
	return sonic.ConfigStd.Marshal(v)
}

func ConvertViaJSON[T any](src interface{}) (T, error) {
	var zero T
	data, err := MarshalSorted(src)
	if err != nil {
		return zero, err
	}
	var result T
	if err := Unmarshal(data, &result); err != nil {
		return zero, err
	}
	return result, nil
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

		rv := reflect.ValueOf(v)
		if rv.Kind() == reflect.Ptr {
			if rv.IsNil() {
				return nil
			}
			rv = rv.Elem()
		}
		if rv.Kind() == reflect.Struct {

			data, err := sonic.Marshal(v)
			if err != nil {
				return v
			}
			var m map[string]interface{}
			if err := sonic.Unmarshal(data, &m); err != nil {
				return v
			}

			return normalizeForSortedMarshal(m)
		}
		return v
	}
}
