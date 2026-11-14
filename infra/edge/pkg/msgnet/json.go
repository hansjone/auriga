// Package projection implements the fixed message-network local material protocol.
package msgnet

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Canonical rejects duplicate keys before canonicalizing. Protocol objects have
// integer numeric fields; strings and object keys follow RFC8785 escaping/order.
func Canonical(raw []byte) ([]byte, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("projection: invalid UTF-8")
	}
	for j := 0; j < len(raw); j++ {
		if raw[j] != '"' {
			continue
		}
		j++
		for j < len(raw) && raw[j] != '"' {
			if raw[j] == '\\' {
				j++
				if j < len(raw) && raw[j] == 'u' {
					if j+4 >= len(raw) {
						return nil, errors.New("projection: truncated Unicode escape")
					}
					n, e := strconv.ParseUint(string(raw[j+1:j+5]), 16, 16)
					if e != nil {
						return nil, e
					}
					j += 4
					if n >= 0xd800 && n <= 0xdbff {
						if j+6 >= len(raw) || string(raw[j+1:j+3]) != `\u` {
							return nil, errors.New("projection: unpaired surrogate")
						}
						low, e := strconv.ParseUint(string(raw[j+3:j+7]), 16, 16)
						if e != nil || low < 0xdc00 || low > 0xdfff {
							return nil, errors.New("projection: unpaired surrogate")
						}
						j += 6
					} else if n >= 0xdc00 && n <= 0xdfff {
						return nil, errors.New("projection: unpaired surrogate")
					}
				}
			}
			j++
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, e := value(d)
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, errors.New("projection: trailing JSON")
	}
	return encode(v)
}
func value(d *json.Decoder) (any, error) {
	t, e := d.Token()
	if e != nil {
		return nil, e
	}
	switch t {
	case json.Delim('{'):
		m := map[string]any{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return nil, e
			}
			s, ok := k.(string)
			if !ok {
				return nil, errors.New("projection: invalid key")
			}
			if _, ok = m[s]; ok {
				return nil, errors.New("projection: duplicate key")
			}
			v, e := value(d)
			if e != nil {
				return nil, e
			}
			m[s] = v
		}
		_, e = d.Token()
		return m, e
	case json.Delim('['):
		a := []any{}
		for d.More() {
			v, e := value(d)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		_, e = d.Token()
		return a, e
	default:
		return t, nil
	}
}
func encode(v any) ([]byte, error) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := utf16.Encode([]rune(keys[i])), utf16.Encode([]rune(keys[j]))
			for n := 0; n < len(a) && n < len(b); n++ {
				if a[n] != b[n] {
					return a[n] < b[n]
				}
			}
			return len(a) < len(b)
		})
		var b bytes.Buffer
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			q, _ := encode(k)
			b.Write(q)
			b.WriteByte(':')
			q, e := encode(x[k])
			if e != nil {
				return nil, e
			}
			b.Write(q)
		}
		b.WriteByte('}')
		return b.Bytes(), nil
	case []any:
		var b bytes.Buffer
		b.WriteByte('[')
		for i, v := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			q, e := encode(v)
			if e != nil {
				return nil, e
			}
			b.Write(q)
		}
		b.WriteByte(']')
		return b.Bytes(), nil
	case json.Number:
		// All current production wire numbers are small integral constants/limits.
		n, e := strconv.ParseInt(string(x), 10, 64)
		if e != nil || n > 9007199254740991 || n < -9007199254740991 {
			return nil, errors.New("projection: nonintegral JSON number")
		}
		return []byte(strconv.FormatInt(n, 10)), nil
	default:
		var b bytes.Buffer
		e := json.NewEncoder(&b)
		e.SetEscapeHTML(false)
		if err := e.Encode(v); err != nil {
			return nil, err
		}
		s := strings.TrimSuffix(b.String(), "\n")
		s = strings.ReplaceAll(strings.ReplaceAll(s, `\u2028`, "\u2028"), `\u2029`, "\u2029")
		return []byte(s), nil
	}
}
func Bytes(v any) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	return Canonical(b)
}
func Digest(b []byte) string             { return fmt.Sprintf("sha256:%x", sha256.Sum256(b)) }
func ObjectDigest(v any) (string, error) { b, e := Bytes(v); return Digest(b), e }
func Decode(raw []byte, out any, canonical bool) error {
	c, e := Canonical(raw)
	if e != nil {
		return e
	}
	if canonical && !bytes.Equal(c, raw) {
		return errors.New("projection: noncanonical JSON")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(out)
}
