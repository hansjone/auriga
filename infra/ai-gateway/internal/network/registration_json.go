package network

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// LocalRegister's consumer preflight follows contracts/network/README.md §1.
// Inspect unknown optional values too, before extracting any known fields. This
// validator neither rewrites the frame nor supplies a canonical signing body.
func validateRegistrationJSON(raw []byte) error {
	if len(raw) == 0 || len(raw) > EncodingMaxPayloadBytes || !utf8.Valid(raw) || !registrationUnicode(raw) {
		return errUnauthorized
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	values := 0
	if err := registrationValue(d, 1, &values); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errUnauthorized
	}
	return nil
}

func registrationValue(d *json.Decoder, depth int, values *int) error {
	if depth > 16 || *values >= 8192 {
		return errUnauthorized
	}
	*values++
	token, err := d.Token()
	if err != nil {
		return errUnauthorized
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			keys := make(map[string]bool)
			for d.More() {
				token, err := d.Token()
				key, ok := token.(string)
				if err != nil || !ok || len(key) > 16384 || keys[key] || len(keys) >= 128 {
					return errUnauthorized
				}
				keys[key] = true
				if err := registrationValue(d, depth+1, values); err != nil {
					return err
				}
			}
			if end, err := d.Token(); err != nil || end != json.Delim('}') {
				return errUnauthorized
			}
		case '[':
			items := 0
			for d.More() {
				items++
				if items > 256 {
					return errUnauthorized
				}
				if err := registrationValue(d, depth+1, values); err != nil {
					return err
				}
			}
			if end, err := d.Token(); err != nil || end != json.Delim(']') {
				return errUnauthorized
			}
		default:
			return errUnauthorized
		}
	case string:
		if len(value) > 16384 {
			return errUnauthorized
		}
	case json.Number:
		n, err := strconv.ParseInt(string(value), 10, 64)
		if err != nil || strings.ContainsAny(string(value), ".eE") || n < -9007199254740991 || n > 9007199254740991 {
			return errUnauthorized
		}
	}
	return nil
}

// encoding/json repairs lone surrogate escapes. Reject them before decoding;
// escaped backslashes remain literal and valid surrogate pairs stay untouched.
func registrationUnicode(raw []byte) bool {
	for i := 0; i+1 < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		if raw[i+1] != 'u' {
			i++
			continue
		}
		if i+6 > len(raw) {
			return false
		}
		n, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
		if err != nil {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+12 > len(raw) || string(raw[i+6:i+8]) != `\u` {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+8:i+12]), 16, 16)
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
