package modelprotocol

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestContentFilterTerminal(t *testing.T) {
	filteredJSON := strings.Replace(completion, `"stop"`, `"content_filter"`, 1)
	filteredEnd := strings.Replace(last, `"stop"`, `"content_filter"`, 1)
	usage := `,"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}`
	for _, format := range []string{"JSON", "SSE"} {
		for _, evidence := range []string{"trusted", "untrusted", "missing", "malformed", "overOutput"} {
			t.Run(format+"/"+evidence, func(t *testing.T) {
				p := syntheticPolicy()
				if evidence == "untrusted" {
					p.VerifyUsage = nil
				}
				raw := filteredJSON
				if format == "SSE" {
					raw = filteredEnd
				}
				switch evidence {
				case "missing":
					raw = strings.Replace(raw, usage, "", 1)
				case "malformed":
					raw = strings.Replace(raw, `"total_tokens":12`, `"total_tokens":11`, 1)
				case "overOutput":
					raw = strings.Replace(raw, `"completion_tokens":2,"total_tokens":12`, `"completion_tokens":9,"total_tokens":19`, 1)
				}
				var result Result
				var err error
				var out bytes.Buffer
				if format == "JSON" {
					result, err = Complete(t.Context(), io.NopCloser(strings.NewReader(raw)), request(t, false, false, p), limits())
				} else {
					result, err = Stream(t.Context(), io.NopCloser(strings.NewReader(sse(first, raw, "[DONE]"))), request(t, true, false, p), limits(), "local", collect(&out))
				}
				valid := evidence == "trusted" || (format == "SSE" && (evidence == "missing" || evidence == "untrusted"))
				if !valid {
					code := "ai.upstream_rejected"
					if format == "SSE" {
						code = "ai.stream_interrupted"
					}
					requireCode(t, err, code)
					if result.Completed || result.UsageKnown || result.Usage != nil || strings.Contains(out.String(), "[DONE]") {
						t.Fatal("invalid usage succeeded")
					}
					return
				}
				if err != nil || !result.Completed || result.UsageKnown != (evidence == "trusted") {
					t.Fatalf("filter completion: %+v %v", result, err)
				}
				if evidence == "trusted" {
					if result.Usage == nil || result.Usage.PromptTokens != 10 || result.Usage.CompletionTokens != 2 || result.Usage.TotalTokens != 12 {
						t.Fatalf("lost final usage: %+v", result.Usage)
					}
				} else if result.Usage != nil || strings.Contains(out.String(), `"usage"`) {
					t.Fatal("invented or untrusted usage")
				}
				if format == "JSON" {
					if result.Completion == nil || result.Completion.Choices[0].FinishReason != "content_filter" {
						t.Fatal("finish reason changed")
					}
				} else if !strings.Contains(out.String(), `"finish_reason":"content_filter"`) || strings.Count(out.String(), "[DONE]") != 1 || strings.Contains(out.String(), `"error"`) {
					t.Fatalf("filter output changed: %s", out.String())
				}
			})
		}
	}
}

func TestLiveCRFrameDelivery(t *testing.T) {
	for _, mode := range []string{"CR", "splitCRLF"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			reader, writer := io.Pipe()
			seen := make(chan []byte, 4)
			type outcome struct {
				result Result
				err    error
			}
			done := make(chan outcome, 1)
			r := request(t, true, false, syntheticPolicy())
			go func() {
				result, err := Stream(ctx, reader, r, limits(), "local", func(_ context.Context, b []byte) (int, error) { seen <- bytes.Clone(b); return len(b), nil })
				done <- outcome{result, err}
			}()
			returned := false
			defer func() {
				cancel()
				_ = writer.Close()
				if !returned {
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("stream goroutine did not exit")
					}
				}
			}()
			for i, payload := range []string{first, last, "[DONE]"} {
				frame := "data: " + payload + "\r\r"
				if mode == "splitCRLF" {
					frame = "data: " + payload + "\r\n\r"
					if i > 0 {
						frame = "\n" + frame
					} // Prior optional LF arrives only after delivery.
				}
				if _, err := io.WriteString(writer, frame); err != nil {
					t.Fatal(err)
				}
				// Writer stays open and sends no next byte until this completed event arrives.
				select {
				case b := <-seen:
					if bytes.Contains(b, []byte("[DONE]")) != (i == 2) {
						t.Fatalf("unexpected event: %s", b)
					}
				case <-time.After(500 * time.Millisecond):
					t.Fatal("complete CR-terminated event waits for another byte")
				}
			}
			select {
			case got := <-done:
				returned = true
				if got.err != nil || !got.result.Completed || !got.result.UsageKnown {
					t.Fatalf("live stream: %+v %v", got.result, got.err)
				}
			case <-time.After(time.Second):
				t.Fatal("CR-terminated DONE waits for another byte")
			}
			if _, err := writer.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("body was not closed: %v", err)
			}
		})
	}
}

func TestTrailingLFEventBound(t *testing.T) {
	for _, extra := range []int{0, 1} {
		frame := "data: " + first + strings.Repeat(" ", EventLimit-len("data: "+first)-4+extra) + "\r\n\r\n"
		var out bytes.Buffer
		result, err := Stream(t.Context(), io.NopCloser(strings.NewReader(frame+sse(last, "[DONE]"))), request(t, true, false, syntheticPolicy()), limits(), "local", collect(&out))
		if extra == 0 {
			if err != nil || !result.Completed {
				t.Fatalf("exact CRLF event bound: %+v %v", result, err)
			}
		} else {
			requireCode(t, err, "ai.stream_interrupted")
			if result.Completed || strings.Contains(out.String(), "[DONE]") {
				t.Fatal("trailing LF escaped the previous event limit")
			}
		}
	}
	// Skipped CRLF bytes still consume the overall input budget.
	wire := strings.ReplaceAll(sse(first), "\n", "\r\n") + sse(last, "[DONE]")
	l := limits()
	l.MaxBytes = int64(len(wire)) - 1
	var out bytes.Buffer
	result, err := Stream(t.Context(), io.NopCloser(strings.NewReader(wire)), request(t, true, false, syntheticPolicy()), l, "local", collect(&out))
	requireCode(t, err, "ai.stream_interrupted")
	if result.Completed || strings.Contains(out.String(), "[DONE]") {
		t.Fatal("optional LF escaped the total input limit")
	}
}
