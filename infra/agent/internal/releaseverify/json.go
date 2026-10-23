package releaseverify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxJSONDepth counts containers, including the top-level object.
const MaxJSONDepth = 32

func strictJSON(raw []byte, limit int) (map[string]any, error) {
	if len(raw) == 0 || len(raw) > limit || !utf8.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON size or UTF-8")
	}
	if err := unicodeEscapes(raw); err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := jsonValue(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("JSON object required")
	}
	return m, nil
}

func jsonValue(d *json.Decoder, depth int) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	if depth >= MaxJSONDepth {
		return nil, fmt.Errorf("JSON depth exceeded")
	}
	switch delim {
	case '{':
		m := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			k, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("invalid object key")
			}
			if _, exists := m[k]; exists {
				return nil, fmt.Errorf("duplicate JSON key")
			}
			v, err := jsonValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			m[k] = v
		}
		if end, err := d.Token(); err != nil || end != json.Delim('}') {
			return nil, fmt.Errorf("invalid JSON object")
		}
		return m, nil
	case '[':
		a := []any{}
		for d.More() {
			v, err := jsonValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		if end, err := d.Token(); err != nil || end != json.Delim(']') {
			return nil, fmt.Errorf("invalid JSON array")
		}
		return a, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter")
	}
}

// encoding/json replaces unpaired UTF-16 surrogate escapes with U+FFFD.
// Preflight them before parsing to avoid accepting two different interpretations.
func unicodeEscapes(raw []byte) error {
	inside := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inside = !inside
			continue
		}
		if !inside || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return fmt.Errorf("truncated escape")
		}
		if raw[i] != 'u' {
			continue
		}
		u, err := hexEscape(raw, i)
		if err != nil {
			return err
		}
		i += 4
		if u >= 0xdc00 && u <= 0xdfff {
			return fmt.Errorf("unpaired low surrogate")
		}
		if u < 0xd800 || u > 0xdbff {
			continue
		}
		if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return fmt.Errorf("unpaired high surrogate")
		}
		lo, err := hexEscape(raw, i+2)
		if err != nil || lo < 0xdc00 || lo > 0xdfff {
			return fmt.Errorf("invalid surrogate pair")
		}
		i += 6
	}
	return nil
}

func hexEscape(raw []byte, u int) (uint64, error) {
	if u+4 >= len(raw) {
		return 0, fmt.Errorf("truncated Unicode escape")
	}
	n, err := strconv.ParseUint(string(raw[u+1:u+5]), 16, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid Unicode escape")
	}
	return n, nil
}

func text(m map[string]any, key string) string { s, _ := m[key].(string); return s }

// encoding/json matches struct members using Unicode simple folding, including
// long-s and Kelvin sign. A differently spelled member must not be mistaken for
// an ignored extension here while an Engine decodes it as a structural field.
// Call only for structural objects; user-controlled map keys remain exact.
func canonicalMembers(m map[string]any, names ...string) error {
	for key := range m {
		for _, name := range names {
			if key != name && strings.EqualFold(key, name) {
				return fmt.Errorf("noncanonical structural JSON member %q (expected %q)", key, name)
			}
		}
	}
	return nil
}

func hasAny(m map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, exists := m[key]; exists {
			return true
		}
	}
	return false
}
func object(m map[string]any, key string) (map[string]any, bool) {
	v, ok := m[key].(map[string]any)
	return v, ok
}
func array(m map[string]any, key string) ([]any, bool) { v, ok := m[key].([]any); return v, ok }
func integer(m map[string]any, key string) (uint64, bool) {
	n, ok := m[key].(json.Number)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseUint(string(n), 10, 64)
	return v, err == nil && strconv.FormatUint(v, 10) == string(n)
}
func only(m map[string]any, keys ...string) bool {
	for k := range m {
		found := false
		for _, allowed := range keys {
			if k == allowed {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func stringMap(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	for _, v := range m {
		if _, ok := v.(string); !ok {
			return false
		}
	}
	return true
}
func stringsArray(v any) bool {
	a, ok := v.([]any)
	if !ok {
		return false
	}
	for _, v := range a {
		if _, ok := v.(string); !ok {
			return false
		}
	}
	return true
}
