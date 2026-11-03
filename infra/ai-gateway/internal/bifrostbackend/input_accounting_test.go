package bifrostbackend

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBudgetedInputOnlyAdmitsBoundedTextShapes(t *testing.T) {
	for _, content := range []string{
		`"the literal image_url and input_audio are just text"`,
		`null`,
		`[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}]`,
		`[{"type":"refusal","refusal":"No"}]`,
	} {
		raw := []byte(`{"model":"auto","messages":[{"role":"user","content":` + content + `}],"max_tokens":1}`)
		if n, err := inputTokenEstimate(raw, false); err != nil || n <= 0 {
			t.Fatalf("bounded text rejected: %s %d %v", content, n, err)
		}
	}
	for _, content := range []string{
		`[{"type":"image_url","image_url":{"url":"https://media.example.com/large.png"}}]`,
		`[{"type":"input_audio","input_audio":{"data":"small-url-or-base64","format":"wav"}}]`,
		`[{"type":"video_url","video_url":{"url":"https://media.example.com/large.mp4"}}]`,
		`[{"type":"file","file":{"file_data":"small-reference"}}]`,
		`[{"type":"text","text":"hello","image_url":{"url":"https://media.example.com/large.png"}}]`,
		`[{"type":"unknown","text":"hello"}]`,
		`{"image_url":"https://media.example.com/large.png"}`,
	} {
		raw := []byte(`{"model":"auto","messages":[{"role":"user","content":` + content + `}],"max_tokens":1}`)
		if n, err := inputTokenEstimate(raw, false); n != 0 || err != invalid {
			t.Fatalf("unbounded input acquired a budget estimate: %s %d %v", content, n, err)
		}
		if n, err := inputTokenEstimate(raw, true); err != nil || n <= 0 {
			t.Fatal("metering-only upstream semantics were restricted")
		}
	}
	w := httptest.NewRecorder()
	rejectInputAccounting(w)
	var problem struct {
		Code   string            `json:"code"`
		Params map[string]string `json:"params"`
	}
	if json.Unmarshal(w.Body.Bytes(), &problem) != nil || w.Code != 400 || problem.Code != string(invalid) || problem.Params["field"] != "/messages" || strings.Contains(w.Body.String(), "media.example.com") {
		t.Fatal("input accounting rejection lost its safe field or leaked a target")
	}
}
