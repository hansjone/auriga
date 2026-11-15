package msgnet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

func encodeCanonical(value any) ([]byte, error) {
	var out bytes.Buffer
	var encode func(any) error
	encode = func(v any) error {
		switch x := v.(type) {
		case nil:
			out.WriteString("null")
		case bool:
			out.WriteString(strconv.FormatBool(x))
		case string:
			if !utf8.ValidString(x) {
				return fmt.Errorf("invalid UTF-8")
			}
			out.WriteByte('"')
			for _, r := range x {
				switch r {
				case '"', '\\':
					out.WriteByte('\\')
					out.WriteRune(r)
				case '\b':
					out.WriteString(`\b`)
				case '\f':
					out.WriteString(`\f`)
				case '\n':
					out.WriteString(`\n`)
				case '\r':
					out.WriteString(`\r`)
				case '\t':
					out.WriteString(`\t`)
				default:
					if r < 32 {
						fmt.Fprintf(&out, `\u%04x`, r)
					} else {
						out.WriteRune(r)
					}
				}
			}
			out.WriteByte('"')
		case json.Number:
			n, err := strconv.ParseUint(string(x), 10, 64)
			if err != nil || n > 9007199254740991 {
				return fmt.Errorf("unsafe signed counter")
			}
			out.WriteString(string(x))
		case []any:
			out.WriteByte('[')
			for i, child := range x {
				if i > 0 {
					out.WriteByte(',')
				}
				if err := encode(child); err != nil {
					return err
				}
			}
			out.WriteByte(']')
		case map[string]any:
			keys := make([]string, 0, len(x))
			for key := range x {
				keys = append(keys, key)
			}
			sort.Slice(keys, func(i, j int) bool {
				a, b := utf16.Encode([]rune(keys[i])), utf16.Encode([]rune(keys[j]))
				for k := 0; k < len(a) && k < len(b); k++ {
					if a[k] != b[k] {
						return a[k] < b[k]
					}
				}
				return len(a) < len(b)
			})
			out.WriteByte('{')
			for i, key := range keys {
				if i > 0 {
					out.WriteByte(',')
				}
				if err := encode(key); err != nil {
					return err
				}
				out.WriteByte(':')
				if err := encode(x[key]); err != nil {
					return err
				}
			}
			out.WriteByte('}')
		default:
			return fmt.Errorf("unsupported signed value")
		}
		return nil
	}
	if err := encode(value); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
