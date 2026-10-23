package releaseverify

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Only strings and safe, nonnegative integers occur in these signed bodies.
// Keeping this encoder deliberately narrow avoids float/protojson conversions.
func canonical(fields map[string]string, numbers map[string]uint64) ([]byte, error) {
	keys := make([]string, 0, len(fields)+len(numbers))
	for k := range fields {
		keys = append(keys, k)
	}
	for k := range numbers {
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
	var out bytes.Buffer
	out.WriteByte('{')
	for i, k := range keys {
		if i != 0 {
			out.WriteByte(',')
		}
		if err := jcsString(&out, k); err != nil {
			return nil, err
		}
		out.WriteByte(':')
		if n, ok := numbers[k]; ok {
			if n > maxSafeInteger {
				return nil, fmt.Errorf("unsafe signed integer")
			}
			out.WriteString(strconv.FormatUint(n, 10))
		} else if err := jcsString(&out, fields[k]); err != nil {
			return nil, err
		}
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

func jcsString(out *bytes.Buffer, s string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("invalid UTF-8 in signed string")
	}
	out.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case '\b':
			out.WriteString(`\b`)
		case '\t':
			out.WriteString(`\t`)
		case '\n':
			out.WriteString(`\n`)
		case '\f':
			out.WriteString(`\f`)
		case '\r':
			out.WriteString(`\r`)
		default:
			if r < 0x20 {
				const hex = "0123456789abcdef"
				out.WriteString(`\u00`)
				out.WriteByte(hex[r>>4])
				out.WriteByte(hex[r&15])
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return nil
}

func timestamp(t *timestamppb.Timestamp) (string, error) {
	if t == nil || t.CheckValid() != nil || len(t.ProtoReflect().GetUnknown()) != 0 {
		return "", fmt.Errorf("invalid signed Timestamp")
	}
	return t.AsTime().UTC().Format("2006-01-02T15:04:05.999999999Z"), nil
}

// Inspect every nested message, including deprecated fields, before interpreting it.
func known(m protoreflect.Message) bool {
	if !m.IsValid() || len(m.GetUnknown()) != 0 {
		return false
	}
	ok := true
	m.Range(func(f protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if f.IsMap() {
			v.Map().Range(func(_ protoreflect.MapKey, x protoreflect.Value) bool {
				if f.MapValue().Message() != nil && !known(x.Message()) {
					ok = false
				}
				return ok
			})
		} else if f.IsList() && f.Message() != nil {
			for i := 0; i < v.List().Len(); i++ {
				if !known(v.List().Get(i).Message()) {
					ok = false
				}
			}
		} else if f.Message() != nil {
			ok = known(v.Message())
		}
		return ok
	})
	return ok
}
