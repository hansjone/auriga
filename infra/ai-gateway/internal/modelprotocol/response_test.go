package modelprotocol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"
)

const completion = `{"id":"c","object":"chat.completion","created":1,"model":"synthetic","choices":[{"index":0,"message":{"role":"assistant","content":"你好"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`
const first = `{"id":"c","object":"chat.completion.chunk","created":1,"model":"synthetic","choices":[{"index":0,"delta":{"role":"assistant","content":"你好"},"finish_reason":null}]}`
const last = `{"id":"c","object":"chat.completion.chunk","created":1,"model":"synthetic","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`

func limits() Limits { return Limits{MaxBytes: 8 << 20, MaxEvents: 1024, MaxEventBytes: EventLimit} }
func request(t *testing.T, stream, tools bool, p Policy) *Request {
	t.Helper()
	raw := strings.Replace(textRequest, `:8`, `:8,"stream":`+map[bool]string{true: "true", false: "false"}[stream], 1)
	if tools {
		raw = strings.Replace(raw, `:8`, `:8,"tools":[{"type":"function","function":{"name":"lookup","parameters":{}}}]`, 1)
	}
	r, err := DecodeRequest(context.Background(), strings.NewReader(raw), p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("data: " + e + "\n\n")
	}
	return b.String()
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

type fragmentReader struct {
	data   []byte
	random *rand.Rand
}

func (r *fragmentReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := min(len(p), 1+r.random.Intn(11), len(r.data))
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}
func collect(out *bytes.Buffer) Sink {
	return func(_ context.Context, b []byte) (int, error) { return out.Write(b) }
}

func TestCompletion(t *testing.T) {
	bodyError := `{"error":{"code":"ai.upstream_timeout","message":"SECRET","type":"upstream_error"},"requestId":"SECRET"}`
	_, upstreamErr := Complete(context.Background(), io.NopCloser(strings.NewReader(bodyError)), request(t, false, false, syntheticPolicy()), limits())
	requireCode(t, upstreamErr, "ai.upstream_timeout")
	for _, trusted := range []bool{true, false} {
		p := syntheticPolicy()
		if !trusted {
			p.VerifyUsage = nil
		}
		body := &trackedBody{Reader: strings.NewReader(completion)}
		r, err := Complete(context.Background(), body, request(t, false, false, p), limits())
		if !body.closed {
			t.Fatal("body not closed")
		}
		if trusted {
			if err != nil || !r.Completed || !r.UsageKnown || r.Usage == nil || r.Completion == nil {
				t.Fatalf("%+v %v", r, err)
			}
			b, e := json.Marshal(r.Completion)
			if e != nil || !parse(b, completionRule, nil) {
				t.Fatal("non D2 output")
			}
		} else {
			requireCode(t, err, "ai.upstream_rejected")
			if r.Usage != nil || r.UsageKnown {
				t.Fatal("invented usage")
			}
		}
	}
	for name, raw := range map[string]string{
		"missingUsage":  strings.Replace(completion, `,"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}`, ``, 1),
		"nullUsage":     strings.Replace(completion, `{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}`, `null`, 1),
		"sum":           strings.Replace(completion, `"total_tokens":12`, `"total_tokens":11`, 1),
		"missingCount":  strings.Replace(completion, `"prompt_tokens":10,`, ``, 1),
		"negative":      strings.Replace(completion, `"completion_tokens":2`, `"completion_tokens":-2`, 1),
		"overOutput":    strings.Replace(completion, `"completion_tokens":2,"total_tokens":12`, `"completion_tokens":9,"total_tokens":19`, 1),
		"overflow":      strings.Replace(completion, `"total_tokens":12`, `"total_tokens":9223372036854775808`, 1),
		"multiple":      strings.Replace(completion, `"index":0`, `"index":1`, 1),
		"unknown":       strings.Replace(completion, `"created":1`, `"created":1,"provider_secret":"SECRET"`, 1),
		"duplicate":     strings.Replace(completion, `"created":1`, `"created":1,"created":1`, 1),
		"nullContent":   strings.Replace(completion, `"你好"`, `null`, 1),
		"model":         strings.Replace(completion, `"synthetic"`, `"other"`, 1),
		"unknownFinish": strings.Replace(completion, `"stop"`, `"unknown_finish"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			body := &trackedBody{Reader: strings.NewReader(raw)}
			r, err := Complete(context.Background(), body, request(t, false, false, syntheticPolicy()), limits())
			requireCode(t, err, "ai.upstream_rejected")
			if r.Completed || r.UsageKnown || !body.closed {
				t.Fatal("false success")
			}
		})
	}
}

func TestExactEventBounds(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		// Entire comment frame, including both separators, is exactly 256 KiB.
		frame := ":" + strings.Repeat("x", EventLimit-1-2*len(newline)) + newline + newline
		for _, extra := range []string{"", "x"} {
			wire := frame + strings.ReplaceAll(sse(first, last, "[DONE]"), "\n", newline)
			if extra != "" {
				wire = "x" + wire
			}
			var out bytes.Buffer
			r, err := Stream(context.Background(), io.NopCloser(strings.NewReader(wire)), request(t, true, false, syntheticPolicy()), limits(), "local", collect(&out))
			if (err == nil) != (extra == "") || r.Completed != (extra == "") {
				t.Fatalf("event byte boundary: %v %v", r, err)
			}
		}
	}
	// Many blank frames count as events, not an unbounded ignorable preamble.
	l := limits()
	l.MaxEvents = 2
	var out bytes.Buffer
	_, err := Stream(context.Background(), io.NopCloser(strings.NewReader("\n\n\n"+sse(first, last, "[DONE]"))), request(t, true, false, syntheticPolicy()), l, "local", collect(&out))
	requireCode(t, err, "ai.stream_interrupted")
}

type oneEventReader struct {
	calls int
	first string
}

func (r *oneEventReader) Read(p []byte) (int, error) {
	r.calls++
	if r.calls == 1 {
		return copy(p, r.first), nil
	}
	return 0, errors.New("read beyond sink backpressure")
}
func TestNoReadAheadDuringSinkBackpressure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &oneEventReader{first: sse(first)}
	_, err := Stream(ctx, io.NopCloser(source), request(t, true, false, syntheticPolicy()), limits(), "local", func(ctx context.Context, _ []byte) (int, error) {
		if source.calls != 1 {
			t.Fatal("prefetched unbounded events")
		}
		cancel()
		return 0, ctx.Err()
	})
	requireCode(t, err, "ai.stream_interrupted")
	if source.calls != 1 {
		t.Fatal("read resumed after cancellation")
	}
	ctx, deadlineCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer deadlineCancel()
	reader, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	_, err = Stream(ctx, reader, request(t, true, false, syntheticPolicy()), limits(), "local", collect(&bytes.Buffer{}))
	requireCode(t, err, "ai.upstream_timeout")
}

func TestStreamFragments(t *testing.T) {
	for seed := int64(0); seed < 30; seed++ {
		for _, newline := range []string{"\n", "\r\n", "\r"} {
			wire := strings.ReplaceAll(sse(first, last, "[DONE]"), "\n", newline)
			body := &trackedBody{Reader: &fragmentReader{data: []byte(wire), random: rand.New(rand.NewSource(seed))}}
			var out bytes.Buffer
			r, err := Stream(context.Background(), body, request(t, true, false, syntheticPolicy()), limits(), "local-request", collect(&out))
			if err != nil || !r.Completed || !r.Started || !r.UsageKnown || !body.closed || strings.Count(out.String(), "[DONE]") != 1 {
				t.Fatalf("seed %d: %+v %v %s", seed, r, err, out.String())
			}
		}
	}
	// SSE comments, metadata and multiline JSON data use the same bounded parser.
	wire := ": hello\r\nid: ignored\r\nevent: message\r\n" + strings.Replace(sse(first), `,"choices"`, ",\ndata: \"choices\"", 1) + sse(last, "[DONE]")
	var out bytes.Buffer
	if _, err := Stream(context.Background(), io.NopCloser(strings.NewReader(wire)), request(t, true, false, syntheticPolicy()), limits(), "local", collect(&out)); err != nil {
		t.Fatal(err)
	}
}

func TestStreamUsageUnknown(t *testing.T) {
	noUsage := strings.Replace(last, `,"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}`, ``, 1)
	for _, p := range []Policy{syntheticPolicy(), func() Policy { p := syntheticPolicy(); p.VerifyUsage = nil; return p }()} {
		end := noUsage
		if p.VerifyUsage == nil {
			end = last
		}
		var out bytes.Buffer
		r, err := Stream(context.Background(), io.NopCloser(strings.NewReader(sse(first, end, "[DONE]"))), request(t, true, false, p), limits(), "local", collect(&out))
		if err != nil || !r.Completed || r.UsageKnown || r.Usage != nil || strings.Contains(out.String(), `"usage"`) {
			t.Fatalf("unknown usage: %+v %v %s", r, err, out.String())
		}
	}
}

func TestStreamFailures(t *testing.T) {
	for name, events := range map[string][]string{
		"missingDone": {first, last}, "earlyDone": {first, "[DONE]"},
		"identity":          {first, strings.Replace(last, `"id":"c"`, `"id":"different"`, 1), "[DONE]"},
		"created":           {first, strings.Replace(last, `"created":1`, `"created":2`, 1), "[DONE]"},
		"afterFinish":       {first, last, first, "[DONE]"},
		"usageBeforeFinish": {strings.Replace(first, `"choices":`, `"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12},"choices":`, 1), "[DONE]"},
		"duplicateUsage":    {first, last, `{"id":"c","object":"chat.completion.chunk","created":1,"model":"synthetic","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`, "[DONE]"},
		"badUsage":          {first, strings.Replace(last, `"total_tokens":12`, `"total_tokens":0`, 1), "[DONE]"},
		"nullUsage":         {first, strings.Replace(last, `{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}`, `null`, 1), "[DONE]"},
		"unknownFinish":     {first, strings.Replace(last, `"stop"`, `"unknown_finish"`, 1), "[DONE]"},
		"upstreamError":     {first, `{"error":{"code":"ai.upstream_rejected","message":"SECRET","type":"upstream_error"},"requestId":"SECRET"}`, "[DONE]"},
		"badUTF8":           {strings.Replace(first, "你好", string([]byte{0xff}), 1), last, "[DONE]"},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			body := &trackedBody{Reader: strings.NewReader(sse(events...))}
			r, err := Stream(context.Background(), body, request(t, true, false, syntheticPolicy()), limits(), "local", collect(&out))
			if err == nil || r.Completed || !body.closed || strings.Contains(out.String(), "[DONE]") || strings.Contains(out.String(), "SECRET") {
				t.Fatalf("false success/leak %+v %v %s", r, err, out.String())
			}
			if r.Started && !strings.Contains(out.String(), `"requestId":"local"`) {
				t.Fatal("missing local error")
			}
		})
	}
	for name, wire := range map[string]string{"unterminated": sse(first, last) + "data: [DONE]", "unfinishedSeparator": sse(first, last) + "data: [DONE]\n", "invalidField": "SECRET\n\n"} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			r, err := Stream(context.Background(), io.NopCloser(strings.NewReader(wire)), request(t, true, false, syntheticPolicy()), limits(), "local", collect(&out))
			requireCode(t, err, "ai.stream_interrupted")
			if r.Completed || strings.Contains(out.String(), "[DONE]") {
				t.Fatal("false done")
			}
		})
	}
}

func TestBoundedResponse(t *testing.T) {
	raw := strings.Replace(completion, "你好", "<", 1)
	ljson := limits()
	ljson.MaxBytes = int64(len(raw))
	_, encodeErr := Complete(context.Background(), io.NopCloser(strings.NewReader(raw)), request(t, false, false, syntheticPolicy()), ljson)
	if encodeErr == nil {
		t.Fatal("JSON output expansion bypasses total limit")
	}
	for _, l := range []Limits{{}, {MaxBytes: 1, MaxEvents: 1, MaxEventBytes: 1}, {MaxBytes: 8 << 20, MaxEvents: 1, MaxEventBytes: EventLimit}, {MaxBytes: 8 << 20, MaxEvents: 100, MaxEventBytes: EventLimit + 1}, {MaxBytes: int64(len(sse(first, last, "[DONE]"))) - 1, MaxEvents: 10, MaxEventBytes: EventLimit}} {
		var out bytes.Buffer
		body := &trackedBody{Reader: strings.NewReader(sse(first, last, "[DONE]"))}
		r, err := Stream(context.Background(), body, request(t, true, false, syntheticPolicy()), l, "local", collect(&out))
		if err == nil || r.Completed || !body.closed || strings.Contains(out.String(), "[DONE]") {
			t.Fatal("limit bypass")
		}
	}
	var out bytes.Buffer
	_, err := Stream(context.Background(), io.NopCloser(strings.NewReader(":"+strings.Repeat("x", EventLimit)+"\n\n"+sse(first, last, "[DONE]"))), request(t, true, false, syntheticPolicy()), limits(), "local", collect(&out))
	requireCode(t, err, "ai.stream_interrupted")
	l := limits()
	l.MaxBytes = int64(len(completion)) - 1
	body := &trackedBody{Reader: strings.NewReader(completion)}
	_, err = Complete(context.Background(), body, request(t, false, false, syntheticPolicy()), l)
	if err == nil || !body.closed {
		t.Fatal("JSON limit bypass")
	}
}

func TestToolFragments(t *testing.T) {
	a := `{"id":"c","object":"chat.completion.chunk","created":1,"model":"synthetic","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"t","type":"function","function":{"name":"look","arguments":"{\""}}]},"finish_reason":null}]}`
	b := `{"id":"c","object":"chat.completion.chunk","created":1,"model":"synthetic","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"up","arguments":"x\":1}"}}]},"finish_reason":"tool_calls"}]}`
	for name, events := range map[string][]string{"valid": {a, b, "[DONE]"}, "missingArguments": {a, strings.Replace(b, `x\":1}`, `x`, 1), "[DONE]"}, "changedID": {a, strings.Replace(b, `"index":0,"function"`, `"index":0,"id":"other","function"`, 1), "[DONE]"}, "wrongFinish": {a, strings.Replace(b, `"tool_calls"}]`, `"stop"}]`, 1), "[DONE]"}, "unknownName": {a, strings.Replace(b, `"up"`, `"bad"`, 1), "[DONE]"}} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			r, err := Stream(context.Background(), io.NopCloser(strings.NewReader(sse(events...))), request(t, true, true, syntheticPolicy()), limits(), "local", collect(&out))
			if name == "valid" {
				if err != nil || !r.Completed {
					t.Fatalf("%+v %v", r, err)
				}
			} else if err == nil || strings.Contains(out.String(), "[DONE]") {
				t.Fatalf("bad tools succeeded %+v %v", r, err)
			}
		})
	}
	toolJSON := strings.Replace(completion, `{"role":"assistant","content":"你好"}`, `{"role":"assistant","content":null,"tool_calls":[{"id":"t","type":"function","function":{"name":"lookup","arguments":"{}"}}]}`, 1)
	toolJSON = strings.Replace(toolJSON, `"stop"`, `"tool_calls"`, 1)
	if _, err := Complete(context.Background(), io.NopCloser(strings.NewReader(toolJSON)), request(t, false, true, syntheticPolicy()), limits()); err != nil {
		t.Fatal(err)
	}
}

func TestCancellationAndBackpressure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	r := request(t, true, false, syntheticPolicy())
	go func() {
		_, err := Stream(ctx, reader, r, limits(), "local", func(context.Context, []byte) (int, error) { return 0, nil })
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		requireCode(t, err, "ai.stream_interrupted")
	case <-time.After(time.Second):
		t.Fatal("cancel blocked reader")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	entered := make(chan struct{})
	done = make(chan error, 1)
	reader, writer = io.Pipe()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _, _ = io.WriteString(writer, sse(first, last, "[DONE]")); _ = writer.Close() }()
	go func() {
		_, err := Stream(ctx, reader, r, limits(), "local", func(ctx context.Context, _ []byte) (int, error) { close(entered); <-ctx.Done(); return 0, ctx.Err() })
		done <- err
	}()
	<-entered
	cancel()
	select {
	case err := <-done:
		requireCode(t, err, "ai.stream_interrupted")
	case <-time.After(time.Second):
		t.Fatal("cancel blocked sink")
	}
	wg.Wait()
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := Complete(ctx, io.NopCloser(strings.NewReader(completion)), request(t, false, false, syntheticPolicy()), limits())
	requireCode(t, err, "ai.upstream_timeout")
}

type errorReader struct{ done bool }

func (r *errorReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, errors.New("SECRET")
	}
	r.done = true
	return copy(p, []byte(first)), errors.New("SECRET")
}
func TestIOFailuresAndNoReplay(t *testing.T) {
	for _, sink := range []Sink{func(context.Context, []byte) (int, error) { return 0, errors.New("SECRET") }, func(context.Context, []byte) (int, error) { return 1, nil }} {
		body := &trackedBody{Reader: strings.NewReader(sse(first, last, "[DONE]"))}
		r, err := Stream(context.Background(), body, request(t, true, false, syntheticPolicy()), limits(), "local", sink)
		requireCode(t, err, "ai.stream_interrupted")
		if r.Completed || !body.closed {
			t.Fatal("write failure success")
		}
	}
	var out bytes.Buffer
	body := &trackedBody{Reader: &errorReader{}}
	_, err := Stream(context.Background(), body, request(t, true, false, syntheticPolicy()), limits(), "local", collect(&out))
	requireCode(t, err, "ai.stream_interrupted")
	if !body.closed || strings.Contains(out.String(), "SECRET") {
		t.Fatal("read error leaked")
	}
	// A fake dispatch owns exactly one body. Protocol processing never calls it.
	calls := 0
	dispatch := func() io.ReadCloser { calls++; return io.NopCloser(strings.NewReader(sse(first))) }
	_, err = Stream(context.Background(), dispatch(), request(t, true, false, syntheticPolicy()), limits(), "local", collect(&out))
	if err == nil || calls != 1 {
		t.Fatal("replay")
	}
}
