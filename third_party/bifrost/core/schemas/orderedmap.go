package schemas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"
)

var jsonSchemaPriority = map[string]int{
	"type":        0,
	"description": 1,
	"properties":  2,
	"required":    3,
}

type Pair struct {
	Key   string
	Value interface{}
}
type OrderedMap struct {
	keys   []string
	values map[string]interface{}
}

var jsonSafeSet = func() [utf8.RuneSelf]bool {
	var s [utf8.RuneSelf]bool
	for c := 0x20; c < utf8.RuneSelf; c++ {
		s[c] = true
	}
	s['"'] = false
	s['\\'] = false
	s['<'] = false
	s['>'] = false
	s['&'] = false
	return s
}()

func KV(key string, value interface{}) Pair {
	return Pair{Key: key, Value: value}
}

func NewOrderedMap() *OrderedMap {
	return &OrderedMap{
		values: make(map[string]interface{}),
	}
}

func NewOrderedMapWithCapacity(cap int) *OrderedMap {
	return &OrderedMap{
		keys:   make([]string, 0, cap),
		values: make(map[string]interface{}, cap),
	}
}

func NewOrderedMapFromPairs(pairs ...Pair) *OrderedMap {
	om := &OrderedMap{
		keys:   make([]string, 0, len(pairs)),
		values: make(map[string]interface{}, len(pairs)),
	}
	for _, p := range pairs {
		om.Set(p.Key, p.Value)
	}
	return om
}

func OrderedMapFromMap(m map[string]interface{}) *OrderedMap {
	if m == nil {
		return nil
	}
	om := &OrderedMap{
		keys:   make([]string, 0, len(m)),
		values: make(map[string]interface{}, len(m)),
	}
	for k, v := range m {
		om.keys = append(om.keys, k)
		om.values[k] = v
	}
	sort.Strings(om.keys)
	return om
}

func (om *OrderedMap) Get(key string) (interface{}, bool) {
	if om == nil {
		return nil, false
	}
	v, ok := om.values[key]
	return v, ok
}

func (om *OrderedMap) Set(key string, value interface{}) {
	if om.values == nil {
		om.values = make(map[string]interface{})
	}
	if _, exists := om.values[key]; !exists {
		om.keys = append(om.keys, key)
	}
	om.values[key] = value
}

func (om *OrderedMap) Delete(key string) {
	if om == nil {
		return
	}
	if _, exists := om.values[key]; !exists {
		return
	}
	delete(om.values, key)
	for i, k := range om.keys {
		if k == key {
			om.keys = append(om.keys[:i], om.keys[i+1:]...)
			break
		}
	}
}

func (om *OrderedMap) Len() int {
	if om == nil {
		return 0
	}
	return len(om.keys)
}

func (om *OrderedMap) Keys() []string {
	if om == nil {
		return nil
	}
	out := make([]string, len(om.keys))
	copy(out, om.keys)
	return out
}

func (om *OrderedMap) Range(fn func(key string, value interface{}) bool) {
	if om == nil {
		return
	}
	for _, k := range om.keys {
		if !fn(k, om.values[k]) {
			break
		}
	}
}

func (om *OrderedMap) Clone() *OrderedMap {
	if om == nil {
		return nil
	}
	clone := &OrderedMap{
		keys:   make([]string, len(om.keys)),
		values: make(map[string]interface{}, len(om.values)),
	}
	copy(clone.keys, om.keys)
	for k, v := range om.values {
		clone.values[k] = v
	}
	return clone
}

func (om *OrderedMap) ToMap() map[string]interface{} {
	if om == nil {
		return nil
	}
	m := make(map[string]interface{}, len(om.values))
	for k, v := range om.values {
		m[k] = v
	}
	return m
}

func (om OrderedMap) MarshalJSON() ([]byte, error) {
	if om.values == nil {
		return []byte("null"), nil
	}
	var buf bytes.Buffer
	if err := om.appendJSON(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (om OrderedMap) appendJSON(dst *bytes.Buffer) error {
	if om.values == nil {
		dst.WriteString("null")
		return nil
	}
	dst.WriteByte('{')
	for i, k := range om.keys {
		if i > 0 {
			dst.WriteByte(',')
		}
		appendJSONString(dst, k)
		dst.WriteByte(':')
		if err := appendValueJSON(dst, om.values[k]); err != nil {
			return err
		}
	}
	dst.WriteByte('}')
	return nil
}

const hexDigits = "0123456789abcdef"

func appendJSONString(dst *bytes.Buffer, s string) {
	dst.WriteByte('"')
	start := 0
	for i := 0; i < len(s); {
		if b := s[i]; b < utf8.RuneSelf {
			if jsonSafeSet[b] {
				i++
				continue
			}
			if start < i {
				dst.WriteString(s[start:i])
			}
			switch b {
			case '\\', '"':
				dst.WriteByte('\\')
				dst.WriteByte(b)
			case '\n':
				dst.WriteString(`\n`)
			case '\r':
				dst.WriteString(`\r`)
			case '\t':
				dst.WriteString(`\t`)
			default:

				dst.WriteString(`\u00`)
				dst.WriteByte(hexDigits[b>>4])
				dst.WriteByte(hexDigits[b&0xF])
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			if start < i {
				dst.WriteString(s[start:i])
			}

			dst.WriteString("\\ufffd")
			i += size
			start = i
			continue
		}
		if r == '\u2028' || r == '\u2029' {
			if start < i {
				dst.WriteString(s[start:i])
			}
			dst.WriteString(`\u202`)
			dst.WriteByte(hexDigits[r&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	if start < len(s) {
		dst.WriteString(s[start:])
	}
	dst.WriteByte('"')
}

func appendValueJSON(dst *bytes.Buffer, v interface{}) error {
	switch val := v.(type) {
	case *OrderedMap:
		if val == nil {
			dst.WriteString("null")
			return nil
		}
		return val.appendJSON(dst)
	case OrderedMap:
		return val.appendJSON(dst)
	case string:
		appendJSONString(dst, val)
		return nil
	default:
		b, err := MarshalSorted(v)
		if err != nil {
			return err
		}
		dst.Write(b)
		return nil
	}
}

func (om *OrderedMap) MarshalSorted() ([]byte, error) {
	if om == nil {
		return []byte("null"), nil
	}

	keys := make([]string, len(om.keys))
	copy(keys, om.keys)
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteByte('{')

	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}

		keyBytes, err := MarshalSorted(k)
		if err != nil {
			return nil, err
		}
		buf.Write(keyBytes)
		buf.WriteByte(':')

		valBytes, err := MarshalSorted(om.values[k])
		if err != nil {
			return nil, err
		}
		buf.Write(valBytes)
	}

	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (om *OrderedMap) UnmarshalJSON(data []byte) error {

	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		om.keys = nil
		om.values = nil
		return nil
	}

	dec := json.NewDecoder(bytes.NewReader(data))

	t, err := dec.Token()
	if err != nil {
		return fmt.Errorf("orderedmap: expected '{': %w", err)
	}
	delim, ok := t.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("orderedmap: expected '{', got %v", t)
	}

	om.keys = om.keys[:0]
	if om.values == nil {
		om.values = make(map[string]interface{})
	} else {
		for k := range om.values {
			delete(om.values, k)
		}
	}

	for dec.More() {

		keyToken, err := dec.Token()
		if err != nil {
			return fmt.Errorf("orderedmap: reading key: %w", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("orderedmap: expected string key, got %T", keyToken)
		}

		value, err := decodeOrderedValue(dec)
		if err != nil {
			return fmt.Errorf("orderedmap: reading value for key %q: %w", key, err)
		}

		om.Set(key, value)
	}

	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("orderedmap: expected '}': %w", err)
	}

	return nil
}

func (om *OrderedMap) SortedCopy() *OrderedMap {
	if om == nil {
		return nil
	}
	if len(om.keys) == 0 {
		return &OrderedMap{values: make(map[string]interface{})}
	}

	newKeys := make([]string, len(om.keys))
	copy(newKeys, om.keys)
	sort.Slice(newKeys, func(i, j int) bool {
		pi, okI := jsonSchemaPriority[newKeys[i]]
		pj, okJ := jsonSchemaPriority[newKeys[j]]
		switch {
		case okI && okJ:
			return pi < pj
		case okI:
			return true
		case okJ:
			return false
		default:
			return newKeys[i] < newKeys[j]
		}
	})

	newValues := make(map[string]interface{}, len(om.values))
	for k, v := range om.values {
		switch nested := v.(type) {
		case *OrderedMap:
			newValues[k] = nested.SortedCopy()
		case map[string]interface{}:
			newValues[k] = OrderedMapFromMap(nested).SortedCopy()
		case []interface{}:
			newValues[k] = sortedCopySlice(nested)
		default:
			newValues[k] = v
		}
	}

	return &OrderedMap{keys: newKeys, values: newValues}
}

func sortedCopySlice(s []interface{}) []interface{} {
	out := make([]interface{}, len(s))
	for i, item := range s {
		switch v := item.(type) {
		case *OrderedMap:
			out[i] = v.SortedCopy()
		case map[string]interface{}:
			out[i] = OrderedMapFromMap(v).SortedCopy()
		case []interface{}:
			out[i] = sortedCopySlice(v)
		default:
			out[i] = item
		}
	}
	return out
}

func (om *OrderedMap) SortedCopyPreservingProperties() *OrderedMap {
	if om == nil {
		return nil
	}
	if len(om.keys) == 0 {
		return &OrderedMap{values: make(map[string]interface{})}
	}

	newKeys := make([]string, len(om.keys))
	copy(newKeys, om.keys)
	sort.Slice(newKeys, func(i, j int) bool {
		pi, okI := jsonSchemaPriority[newKeys[i]]
		pj, okJ := jsonSchemaPriority[newKeys[j]]
		switch {
		case okI && okJ:
			return pi < pj
		case okI:
			return true
		case okJ:
			return false
		default:
			return newKeys[i] < newKeys[j]
		}
	})

	newValues := make(map[string]interface{}, len(om.values))
	for k, v := range om.values {
		if k == "properties" {

			newValues[k] = preserveKeysOrderedCopyWithAwareness(v)
		} else {
			switch nested := v.(type) {
			case *OrderedMap:
				newValues[k] = nested.SortedCopyPreservingProperties()
			case map[string]interface{}:
				newValues[k] = OrderedMapFromMap(nested).SortedCopyPreservingProperties()
			case []interface{}:
				newValues[k] = sortedCopySlicePreservingProperties(nested)
			default:
				newValues[k] = v
			}
		}
	}

	return &OrderedMap{keys: newKeys, values: newValues}
}

func preserveKeysOrderedCopyWithAwareness(v interface{}) interface{} {
	switch om := v.(type) {
	case *OrderedMap:
		return om.preserveKeysWithPropertyAwareness()
	case map[string]interface{}:

		return OrderedMapFromMap(om).SortedCopyPreservingProperties()
	default:
		return v
	}
}

func (om *OrderedMap) preserveKeysWithPropertyAwareness() *OrderedMap {
	if om == nil {
		return nil
	}
	if len(om.keys) == 0 {
		return &OrderedMap{values: make(map[string]interface{})}
	}

	newKeys := make([]string, len(om.keys))
	copy(newKeys, om.keys)

	newValues := make(map[string]interface{}, len(om.values))
	for k, v := range om.values {
		switch nested := v.(type) {
		case *OrderedMap:
			newValues[k] = nested.SortedCopyPreservingProperties()
		case map[string]interface{}:
			newValues[k] = OrderedMapFromMap(nested).SortedCopyPreservingProperties()
		case []interface{}:
			newValues[k] = sortedCopySlicePreservingProperties(nested)
		default:
			newValues[k] = v
		}
	}

	return &OrderedMap{keys: newKeys, values: newValues}
}

func sortedCopySlicePreservingProperties(s []interface{}) []interface{} {
	out := make([]interface{}, len(s))
	for i, item := range s {
		switch v := item.(type) {
		case *OrderedMap:
			out[i] = v.SortedCopyPreservingProperties()
		case map[string]interface{}:
			out[i] = OrderedMapFromMap(v).SortedCopyPreservingProperties()
		case []interface{}:
			out[i] = sortedCopySlicePreservingProperties(v)
		default:
			out[i] = item
		}
	}
	return out
}

func decodeOrderedValue(dec *json.Decoder) (interface{}, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}

	switch v := t.(type) {
	case json.Delim:
		if v == '{' {

			nested := NewOrderedMap()
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("expected string key, got %T", keyToken)
				}
				val, err := decodeOrderedValue(dec)
				if err != nil {
					return nil, err
				}
				nested.Set(key, val)
			}

			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return nested, nil
		}
		if v == '[' {

			var arr []interface{}
			for dec.More() {
				val, err := decodeOrderedValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}

			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			if arr == nil {
				arr = []interface{}{}
			}
			return arr, nil
		}
		return nil, fmt.Errorf("unexpected delimiter: %v", v)

	case string:
		return v, nil
	case float64:
		return v, nil
	case bool:
		return v, nil
	case nil:
		return nil, nil
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return v.String(), nil
		}
		return f, nil
	default:
		return v, nil
	}
}
