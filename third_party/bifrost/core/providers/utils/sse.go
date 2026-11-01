package utils

import (
	"bufio"
	"bytes"
	"io"
	"time"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
)

type defaultSSEEventReader struct {
	scanner   *bufio.Scanner
	eventType string
	eventData []byte
}
type SSEDataReader interface {
	ReadDataLine() ([]byte, error)
}

var sseIDPrefix = []byte("id:")

type SSEReaderFactory struct {
	NewDataReader  func(reader io.Reader) SSEDataReader
	NewEventReader func(reader io.Reader) SSEEventReader
}
type SSEStreamTerminator interface {
	SawDoneMarker() bool
}

var sseRetryPrefix = []byte("retry:")

type SSEEventReader interface {
	ReadEvent() (eventType string, data []byte, err error)
}

var sseDataPrefix = []byte("data:")

type defaultSSEDataReader struct {
	scanner *bufio.Scanner
	pending []byte
	sawDone bool

	endOnComment bool
	sawComment   bool
	commentsSeen int

	ctx *schemas.BifrostContext
}

var sseEventPrefix = []byte("event:")

type SSEPostFinishTerminator interface {
	EndOnCommentAfterFinish()
	EndedOnComment() bool
}

var sseDoneMarker = []byte("[DONE]")

const (
	sseInitialBufSize = 8 * 1024
	sseMaxBufSize     = 10 * 1024 * 1024
)

func SSEStreamEndedOnMarker(r SSEDataReader) bool {
	if t, ok := r.(SSEStreamTerminator); ok {
		return t.SawDoneMarker()
	}
	return true
}

func SSEEndOnCommentAfterFinish(r SSEDataReader) {
	if t, ok := r.(SSEPostFinishTerminator); ok {
		t.EndOnCommentAfterFinish()
	}
}

func SSEEndedOnComment(r SSEDataReader) bool {
	if t, ok := r.(SSEPostFinishTerminator); ok {
		return t.EndedOnComment()
	}
	return false
}

func GetSSEDataReader(ctx *schemas.BifrostContext, reader io.Reader) SSEDataReader {
	if ctx != nil {
		if factory, ok := ctx.Value(schemas.BifrostContextKeySSEReaderFactory).(*SSEReaderFactory); ok && factory != nil && factory.NewDataReader != nil {
			return factory.NewDataReader(reader)
		}
	}
	return newDefaultSSEDataReader(ctx, reader)
}

func GetSSEEventReader(ctx *schemas.BifrostContext, reader io.Reader) SSEEventReader {
	if ctx != nil {
		if factory, ok := ctx.Value(schemas.BifrostContextKeySSEReaderFactory).(*SSEReaderFactory); ok && factory != nil && factory.NewEventReader != nil {
			return factory.NewEventReader(reader)
		}
	}
	return newDefaultSSEEventReader(reader)
}

func (r *defaultSSEDataReader) SawDoneMarker() bool { return r.sawDone }

func (r *defaultSSEDataReader) EndOnCommentAfterFinish() { r.endOnComment = true }

func (r *defaultSSEDataReader) EndedOnComment() bool { return r.sawComment }

func newDefaultSSEDataReader(ctx *schemas.BifrostContext, reader io.Reader) *defaultSSEDataReader {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, sseInitialBufSize), sseMaxBufSize)
	return &defaultSSEDataReader{scanner: scanner, ctx: ctx}
}

func (r *defaultSSEDataReader) ReadDataLine() ([]byte, error) {

	if r.ctx != nil {
		upBefore, _ := schemas.GetUpstreamLatency(r.ctx)
		frameStart := time.Now()
		defer func() {
			upAfter, _ := schemas.GetUpstreamLatency(r.ctx)
			schemas.AddStreamParse(r.ctx, time.Since(frameStart)-(upAfter-upBefore))
		}()
	}
	for {
		line, ok := r.nextLine()
		if !ok {
			break
		}

		if len(line) == 0 {
			continue
		}
		if line[0] == ':' {

			if r.endOnComment {
				r.commentsSeen++
				if r.commentsSeen >= 2 {
					r.sawComment = true
					return nil, io.EOF
				}
			}
			continue
		}

		r.commentsSeen = 0

		if bytes.HasPrefix(line, sseDataPrefix) {
			data := line[5:]
			if len(data) > 0 && data[0] == ' ' {
				data = data[1:]
			}
			if len(data) == 0 {
				continue
			}
			if bytes.Equal(data, sseDoneMarker) {
				r.sawDone = true
				return nil, io.EOF
			}

			return append([]byte(nil), data...), nil
		}

		if bytes.HasPrefix(line, sseEventPrefix) ||
			bytes.HasPrefix(line, sseIDPrefix) ||
			bytes.HasPrefix(line, sseRetryPrefix) {
			continue
		}

		trimmed := bytes.TrimLeft(line, " \t\r")
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') && !sonic.Valid(trimmed) {
			return r.readMultilineJSON(line), nil
		}
		return append([]byte(nil), line...), nil
	}
	if err := r.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (r *defaultSSEDataReader) nextLine() ([]byte, bool) {
	if r.pending != nil {
		line := r.pending
		r.pending = nil
		return line, true
	}
	if r.scanner.Scan() {
		return r.scanner.Bytes(), true
	}
	return nil, false
}

func (r *defaultSSEDataReader) readMultilineJSON(open []byte) []byte {
	buf := append([]byte(nil), open...)
	for r.scanner.Scan() {
		line := r.scanner.Bytes()
		if bytes.HasPrefix(line, sseDataPrefix) {
			r.pending = append([]byte(nil), line...)
			return buf
		}
		buf = append(buf, '\n')
		buf = append(buf, line...)
		if trimmed := bytes.TrimRight(buf, " \t\r\n"); len(trimmed) > 0 &&
			(trimmed[len(trimmed)-1] == '}' || trimmed[len(trimmed)-1] == ']') &&
			sonic.Valid(buf) {
			return buf
		}
		if len(buf) >= sseMaxBufSize {
			return buf
		}
	}
	return buf
}

func newDefaultSSEEventReader(reader io.Reader) *defaultSSEEventReader {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, sseInitialBufSize), sseMaxBufSize)
	return &defaultSSEEventReader{scanner: scanner}
}

func (r *defaultSSEEventReader) ReadEvent() (string, []byte, error) {
	for r.scanner.Scan() {
		line := r.scanner.Bytes()

		if len(line) > 0 && line[0] == ':' {
			continue
		}

		if len(line) == 0 {
			if r.eventType == "" && len(r.eventData) == 0 {
				continue
			}
			eventType := r.eventType
			eventData := make([]byte, len(r.eventData))
			copy(eventData, r.eventData)
			r.eventType = ""
			r.eventData = r.eventData[:0]
			return eventType, eventData, nil
		}

		if bytes.HasPrefix(line, sseEventPrefix) {
			field := line[6:]
			if len(field) > 0 && field[0] == ' ' {
				field = field[1:]
			}
			r.eventType = string(field)
		} else if bytes.HasPrefix(line, sseDataPrefix) {
			data := line[5:]
			if len(data) > 0 && data[0] == ' ' {
				data = data[1:]
			}
			if len(r.eventData) > 0 {
				r.eventData = append(r.eventData, '\n')
			}
			r.eventData = append(r.eventData, data...)
		}

	}

	if r.eventType != "" || len(r.eventData) > 0 {
		eventType := r.eventType
		eventData := make([]byte, len(r.eventData))
		copy(eventData, r.eventData)
		r.eventType = ""
		r.eventData = r.eventData[:0]
		return eventType, eventData, nil
	}

	if err := r.scanner.Err(); err != nil {
		return "", nil, err
	}
	return "", nil, io.EOF
}
