package modelprotocol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

const textRequest = `{"model":"synthetic","messages":[{"role":"user","content":"你好"}],"max_tokens":8}`

// Synthetic model ONLY: one token per serialized byte, plus 7 template tokens.
// This definition is not a tokenizer or a bound for any real provider.
func syntheticPolicy() Policy {
	return Policy{Model: "synthetic", ContextWindow: 1 << 21, MaxOutputTokens: 100,
		Tools: true, Temperature: true, TopP: true, Stop: true,
		MeasureInput: func(_ context.Context, r api.ChatCompletionRequest) (int64, error) {
			b, err := json.Marshal(r)
			return int64(len(b)) + 7, err
		},
		VerifyUsage: func(_ context.Context, _ api.TokenUsage) bool { return true },
	}
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Fatal("error leaks input")
	}
}

func TestRequestValidation(t *testing.T) {
	valid := []string{textRequest,
		`{"model":"synthetic","max_tokens":8,"messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"a","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","content":"ok","tool_call_id":"a"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"n":{"type":"number"}},"additionalProperties":false}}}],"tool_choice":"auto"}`,
	}
	for _, raw := range valid {
		r, err := DecodeRequest(context.Background(), strings.NewReader(raw), syntheticPolicy())
		if err != nil || r.InputBound() <= 7 {
			t.Fatalf("valid request: %v", err)
		}
		if _, err := ValidateRequest(context.Background(), r.Value(), syntheticPolicy()); err != nil {
			t.Fatal(err)
		}
	}
	bad := map[string]string{
		"duplicate":         strings.Replace(textRequest, `"model":`, `"model":"SECRET","model":`, 1),
		"escapedDuplicate":  strings.Replace(textRequest, `"model":`, `"mo\u0064el":"SECRET","model":`, 1),
		"unknown":           strings.Replace(textRequest, `"max_tokens":8`, `"max_tokens":8,"n":2`, 1),
		"missingMax":        strings.Replace(textRequest, `,"max_tokens":8`, ``, 1),
		"nullMax":           strings.Replace(textRequest, `:8`, `:null`, 1),
		"zeroMax":           strings.Replace(textRequest, `:8`, `:0`, 1),
		"overflow":          strings.Replace(textRequest, `:8`, `:9223372036854775808`, 1),
		"unsafeInteger":     strings.Replace(textRequest, `:8`, `:9007199254740992`, 1),
		"fraction":          strings.Replace(textRequest, `:8`, `:1.5`, 1),
		"nontext":           strings.Replace(textRequest, `"你好"`, `[{"type":"image_url"}]`, 1),
		"nullUser":          strings.Replace(textRequest, `"你好"`, `null`, 1),
		"orphan":            strings.Replace(textRequest, `"role":"user"`, `"role":"tool","tool_call_id":"a"`, 1),
		"idOnUser":          strings.Replace(textRequest, `"role":"user"`, `"role":"user","tool_call_id":"a"`, 1),
		"emptyMessages":     `{"model":"synthetic","messages":[],"max_tokens":1}`,
		"badRole":           strings.Replace(textRequest, `"user"`, `"developer"`, 1),
		"badUTF8":           strings.Replace(textRequest, "你好", string([]byte{0xff}), 1),
		"unpairedSurrogate": strings.Replace(textRequest, "你好", `\ud800`, 1),
		"trailing":          textRequest + `{}`,
		"nullStream":        strings.Replace(textRequest, `:8`, `:8,"stream":null`, 1),
		"stopString":        strings.Replace(textRequest, `:8`, `:8,"stop":"x"`, 1),
		"emptyStop":         strings.Replace(textRequest, `:8`, `:8,"stop":[]`, 1),
		"temperature":       strings.Replace(textRequest, `:8`, `:8,"temperature":2.00000001`, 1),
		"topP":              strings.Replace(textRequest, `:8`, `:8,"top_p":-0.01`, 1),
		"choice":            strings.Replace(textRequest, `:8`, `:8,"tool_choice":"custom"`, 1),
		"requiredNoTools":   strings.Replace(textRequest, `:8`, `:8,"tool_choice":"required"`, 1),
		"oversize":          strings.Replace(textRequest, "你好", strings.Repeat("x", 1<<20), 1),
	}
	for name, raw := range bad {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeRequest(context.Background(), strings.NewReader(raw), syntheticPolicy())
			requireCode(t, err, "common.validation_failed")
		})
	}
	for name, raw := range map[string]string{
		"pending":            strings.Replace(valid[1], `,{"role":"tool","content":"ok","tool_call_id":"a"}`, ``, 1),
		"doubleResult":       strings.Replace(valid[1], `"tool_call_id":"a"}`, `"tool_call_id":"a"},{"role":"tool","content":"ok","tool_call_id":"a"}`, 1),
		"duplicateParameter": strings.Replace(valid[1], `"type":"number"`, `"type":"number","type":"SECRET"`, 1),
		"nullParameter":      strings.Replace(valid[1], `"parameters":{"type":"object","properties":{"n":{"type":"number"}},"additionalProperties":false}`, `"parameters":null`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeRequest(context.Background(), strings.NewReader(raw), syntheticPolicy())
			requireCode(t, err, "common.validation_failed")
		})
	}
}

func TestPolicyBounds(t *testing.T) {
	for _, change := range []func(*Policy){
		func(p *Policy) { p.Model = "unknown" }, func(p *Policy) { p.ContextWindow = 0 },
		func(p *Policy) { p.MaxOutputTokens = 0 }, func(p *Policy) { p.MeasureInput = nil },
		func(p *Policy) { p.ContextWindow = 8 }, func(p *Policy) { p.MaxOutputTokens = 7 },
		func(p *Policy) {
			p.MeasureInput = func(context.Context, api.ChatCompletionRequest) (int64, error) { return -1, nil }
		},
		func(p *Policy) {
			p.MeasureInput = func(context.Context, api.ChatCompletionRequest) (int64, error) { return 0, errors.New("SECRET") }
		},
		func(p *Policy) {
			p.MeasureInput = func(context.Context, api.ChatCompletionRequest) (int64, error) { return 9007199254740991, nil }
		},
	} {
		p := syntheticPolicy()
		change(&p)
		_, err := DecodeRequest(context.Background(), strings.NewReader(textRequest), p)
		requireCode(t, err, "common.validation_failed")
	}
	for _, capability := range []string{"temperature", "top_p", "stop", "tools"} {
		raw := strings.Replace(textRequest, `:8`, `:8,"`+capability+`":`+map[string]string{"temperature": "1", "top_p": "0.5", "stop": "[\"x\"]", "tools": "[]"}[capability], 1)
		p := syntheticPolicy()
		p.Temperature = false
		p.TopP = false
		p.Stop = false
		p.Tools = false
		_, err := DecodeRequest(context.Background(), strings.NewReader(raw), p)
		requireCode(t, err, "common.validation_failed")
	}
}

func TestRequestSchemaLimitsAndIsolation(t *testing.T) {
	largeSchema := strings.Replace(textRequest, `:8`, `:8,"tools":[{"type":"function","function":{"name":"lookup","parameters":{"minimum":1e1000}}}]`, 1)
	if _, err := DecodeRequest(context.Background(), strings.NewReader(largeSchema), syntheticPolicy()); err != nil {
		t.Fatalf("valid open schema number rejected: %v", err)
	}
	padding := RequestLimit - len(textRequest) + len("你好")
	exact := strings.Replace(textRequest, "你好", strings.Repeat("x", padding), 1)
	if _, err := DecodeRequest(context.Background(), strings.NewReader(exact), syntheticPolicy()); err != nil {
		t.Fatalf("exact request byte limit: %v", err)
	}
	for _, good := range []string{`\ud83d\ude00`, `\\ud800`} {
		if _, err := DecodeRequest(context.Background(), strings.NewReader(strings.Replace(textRequest, "你好", good, 1)), syntheticPolicy()); err != nil {
			t.Fatal(err)
		}
	}
	for name, raw := range map[string]string{
		"modelLength":        strings.Replace(textRequest, "synthetic", strings.Repeat("m", 129), 1),
		"messages":           strings.Replace(textRequest, `[{"role":"user","content":"你好"}]`, `[`+strings.TrimSuffix(strings.Repeat(`{"role":"user","content":"x"},`, 257), ",")+`]`, 1),
		"stopCount":          strings.Replace(textRequest, `:8`, `:8,"stop":["a","b","c","d","e"]`, 1),
		"stopLength":         strings.Replace(textRequest, `:8`, `:8,"stop":["`+strings.Repeat("你", 129)+`"]`, 1),
		"unknownNested":      strings.Replace(textRequest, `"role":"user"`, `"role":"user","SECRET":true`, 1),
		"surrogateLow":       strings.Replace(textRequest, "你好", `\udfff`, 1),
		"surrogateWrongPair": strings.Replace(textRequest, "你好", `\ud800\u1234`, 1),
		"depth":              strings.Replace(textRequest, `:8`, `:8,"tools":[{"type":"function","function":{"name":"lookup","parameters":{"x":`+strings.Repeat("[", 65)+`0`+strings.Repeat("]", 65)+`}}}]`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeRequest(context.Background(), strings.NewReader(raw), syntheticPolicy())
			requireCode(t, err, "common.validation_failed")
		})
	}
	raw := strings.Replace(textRequest, `:8`, `:8,"tools":[{"type":"function","function":{"name":"lookup","parameters":{"properties":{"n":{"const":9007199254740993}},"max_tokens":{"unknown":true}}}}]`, 1)
	calls := 0
	p := syntheticPolicy()
	measure := p.MeasureInput
	p.MeasureInput = func(ctx context.Context, v api.ChatCompletionRequest) (int64, error) {
		calls++
		if _, ok := v.Tools[0].Function.Parameters["max_tokens"]; !ok {
			t.Fatal("incomplete metering input")
		}
		return measure(ctx, v)
	}
	r, err := DecodeRequest(context.Background(), strings.NewReader(raw), p)
	if err != nil {
		t.Fatal(err)
	}
	v := r.Value()
	v.Tools[0].Function.Parameters["max_tokens"] = "mutated"
	v.Messages[0].Content.Set(nil)
	b, err := json.Marshal(r.Value())
	if err != nil || !strings.Contains(string(b), "9007199254740993") || strings.Contains(string(b), "mutated") || calls != 1 {
		t.Fatalf("snapshot/precision/meter changed: %s %v", b, err)
	}
}
