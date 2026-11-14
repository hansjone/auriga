package projection

import (
	"bytes"
	"io"
	"testing"
)

type fragmented struct{ io.Reader }

func (f fragmented) Read(b []byte) (int, error) {
	if len(b) > 1 {
		b = b[:1]
	}
	return f.Reader.Read(b)
}

type partialWriter struct{ bytes.Buffer }

func (w *partialWriter) Write(b []byte) (int, error) {
	if len(b) > 1 {
		b = b[:1]
	}
	return w.Buffer.Write(b)
}
func TestFraming(t *testing.T) {
	var out partialWriter
	if e := WriteFrame(&out, []byte(`{}`)); e != nil {
		t.Fatal(e)
	}
	raw, e := ReadFrame(fragmented{bytes.NewReader(out.Bytes())})
	if e != nil || string(raw) != "{}" {
		t.Fatal(string(raw), e)
	}
	for _, raw := range [][]byte{{0, 0, 0, 0}, {0, 0, 0, 2, '{'}, {0, 0, 0, 1, '{', 'x'}, {0, 0, 64, 1}} {
		if _, e := ReadFrame(bytes.NewReader(raw)); e == nil {
			t.Fatal("bad frame accepted")
		}
	}
}
