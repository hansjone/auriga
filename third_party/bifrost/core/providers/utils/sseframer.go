// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package utils

import (
	"bytes"
	"unicode/utf8"
	"unsafe"
)

type RuneCounter struct {
	carry    [utf8.UTFMax]byte
	carryLen int
	runes    int64
}
type SSEFramer struct {
	pending bytes.Buffer
}

func (f *SSEFramer) Write(chunk []byte) [][]byte {
	f.pending.Write(chunk)
	var out [][]byte
	for {
		data := f.pending.Bytes()
		idx, delimLen := findFirstSSEFrameDelimiter(data)
		if idx < 0 {
			break
		}
		if p := extractSSEDataPayload(data[:idx]); isSSEEventPayload(p) {
			out = append(out, append([]byte(nil), p...))
		}
		f.pending.Next(idx + delimLen)
	}
	if f.pending.Len() > sseMaxBufSize {
		f.pending.Reset()
	}
	return out
}

func (f *SSEFramer) Flush() []byte {
	defer f.pending.Reset()
	if p := extractSSEDataPayload(f.pending.Bytes()); isSSEEventPayload(p) {
		return append([]byte(nil), p...)
	}
	return nil
}

func isSSEEventPayload(p []byte) bool {
	p = bytes.TrimSpace(p)
	return len(p) > 0 && p[0] != ':'
}

func (c *RuneCounter) Write(chunk []byte) {

	for c.carryLen > 0 && len(chunk) > 0 {
		take := min(utf8.UTFMax-c.carryLen, len(chunk))
		copy(c.carry[c.carryLen:], chunk[:take])
		c.carryLen += take
		chunk = chunk[take:]
		for c.carryLen > 0 && utf8.FullRune(c.carry[:c.carryLen]) {
			_, size := utf8.DecodeRune(c.carry[:c.carryLen])
			c.runes++
			copy(c.carry[:], c.carry[size:c.carryLen])
			c.carryLen -= size
		}
	}
	if c.carryLen > 0 {
		return
	}
	end := len(chunk)
	for k := len(chunk) - 1; k >= 0 && k >= len(chunk)-utf8.UTFMax; k-- {
		if utf8.RuneStart(chunk[k]) {
			if !utf8.FullRune(chunk[k:]) {
				end = k
			}
			break
		}
	}
	c.runes += int64(RuneCount(chunk[:end]))
	c.carryLen = copy(c.carry[:], chunk[end:])
}

func (c *RuneCounter) Total() int64 {
	return c.runes + int64(RuneCount(c.carry[:c.carryLen]))
}

func RuneCount(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	return utf8.RuneCountInString(unsafe.String(unsafe.SliceData(b), len(b)))
}
