package projection

import (
	"encoding/binary"
	"fmt"
	"io"
)

func ReadFrame(input io.Reader) ([]byte, error) {
	var header [4]byte
	if _, e := io.ReadFull(input, header[:]); e != nil {
		return nil, e
	}
	n := binary.BigEndian.Uint32(header[:])
	if n < 1 || n > 16384 {
		return nil, fmt.Errorf("invalid frame size")
	}
	raw := make([]byte, n)
	if _, e := io.ReadFull(input, raw); e != nil {
		return nil, e
	}
	var tail [1]byte
	if n, e := input.Read(tail[:]); n != 0 || e != io.EOF {
		return nil, fmt.Errorf("trailing or incomplete stream")
	}
	return raw, nil
}
func WriteFrame(output io.Writer, raw []byte) error {
	if len(raw) < 1 || len(raw) > 16384 {
		return fmt.Errorf("invalid frame size")
	}
	framed := make([]byte, 4+len(raw))
	binary.BigEndian.PutUint32(framed, uint32(len(raw)))
	copy(framed[4:], raw)
	for len(framed) > 0 {
		n, e := output.Write(framed)
		if e != nil {
			return e
		}
		if n < 1 {
			return io.ErrShortWrite
		}
		framed = framed[n:]
	}
	return nil
}
