package modelprotocol

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const safeInteger int64 = 9007199254740991
const RequestLimit = 1 << 20
const EventLimit = 256 << 10

type rule func(any) bool

func object(required string, fields map[string]rule) rule {
	return func(v any) bool {
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		for _, key := range strings.Fields(required) {
			if _, ok := m[key]; !ok {
				return false
			}
		}
		for key, value := range m {
			if test := fields[key]; test == nil || !test(value) {
				return false
			}
		}
		return true
	}
}
func text(min, max int) rule {
	return func(v any) bool {
		s, ok := v.(string)
		return ok && utf8.RuneCountInString(s) >= min && utf8.RuneCountInString(s) <= max
	}
}
func enum(values ...string) rule {
	return func(v any) bool {
		for _, s := range values {
			if v == s {
				return true
			}
		}
		return false
	}
}
func integer(min, max int64) rule {
	return func(v any) bool {
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		i, err := strconv.ParseInt(string(n), 10, 64)
		return err == nil && i >= min && i <= max
	}
}
func number(max float64) rule {
	return func(v any) bool {
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		f, err := n.Float64()
		return err == nil && f >= 0 && f <= max
	}
}
func array(min, max int, item rule) rule {
	return func(v any) bool {
		a, ok := v.([]any)
		if !ok || len(a) < min || len(a) > max {
			return false
		}
		for _, v := range a {
			if !item(v) {
				return false
			}
		}
		return true
	}
}
func nullable(test rule) rule { return func(v any) bool { return v == nil || test(v) } }

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var nameRule rule = func(v any) bool { s, ok := v.(string); return ok && namePattern.MatchString(s) }
var stringRule = text(0, RequestLimit)
var callRule = object("id type function", map[string]rule{
	"id": text(1, 128), "type": enum("function"),
	"function": object("name arguments", map[string]rule{"name": nameRule, "arguments": text(0, 262144)}),
})
var requestRule = object("model messages max_tokens", map[string]rule{
	"model": text(1, 128), "max_tokens": integer(1, safeInteger),
	"stream":      func(v any) bool { _, ok := v.(bool); return ok },
	"temperature": number(2), "top_p": number(1), "stop": array(1, 4, text(0, 128)),
	"tool_choice": enum("none", "auto", "required"),
	"messages": array(1, 256, object("role content", map[string]rule{
		"role": enum("system", "user", "assistant", "tool"), "content": nullable(stringRule),
		"name": text(0, 64), "tool_call_id": text(1, 128), "tool_calls": array(0, 128, callRule),
	})),
	"tools": array(0, 128, object("type function", map[string]rule{
		"type": enum("function"), "function": object("name parameters", map[string]rule{
			"name": nameRule, "description": text(0, 8192), "parameters": func(v any) bool { _, ok := v.(map[string]any); return ok },
		}),
	})),
})

// Walk tokens before generated decoding: duplicate keys are otherwise silently lost.
func value(d *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, io.ErrUnexpectedEOF
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t {
	case json.Delim('{'):
		m := map[string]any{}
		for d.More() {
			key, e := d.Token()
			if e != nil {
				return nil, e
			}
			k, ok := key.(string)
			if !ok {
				return nil, io.ErrUnexpectedEOF
			}
			if _, ok := m[k]; ok {
				return nil, io.ErrUnexpectedEOF
			}
			v, e := value(d, depth+1)
			if e != nil {
				return nil, e
			}
			m[k] = v
		}
		_, err = d.Token()
		return m, err
	case json.Delim('['):
		a := []any{}
		for d.More() {
			v, e := value(d, depth+1)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		_, err = d.Token()
		return a, err
	default:
		return t, nil
	}
}
func parse(b []byte, test rule, out any) bool {
	if !utf8.Valid(b) || !unicodeEscapes(b) {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	v, err := value(d, 0)
	if err != nil {
		return false
	}
	if _, err = d.Token(); err != io.EOF || !test(v) {
		return false
	}
	return out == nil || json.Unmarshal(b, out) == nil
}

// encoding/json replaces unpaired UTF-16 escapes; reject rather than repair.
func unicodeEscapes(b []byte) bool {
	for i := 0; i+1 < len(b); i++ {
		if b[i] != '\\' {
			continue
		}
		if b[i+1] != 'u' {
			i++
			continue
		}
		if i+6 > len(b) {
			return false
		}
		n, err := strconv.ParseUint(string(b[i+2:i+6]), 16, 16)
		if err != nil {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+12 > len(b) || string(b[i+6:i+8]) != `\u` {
				return false
			}
			low, err := strconv.ParseUint(string(b[i+8:i+12]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 11
		} else {
			if n >= 0xdc00 && n <= 0xdfff {
				return false
			}
			i += 5
		}
	}
	return true
}

func boundedRead(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil || int64(len(b)) > max {
		return nil, io.ErrUnexpectedEOF
	}
	return b, nil
}
