// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
)

type RealtimeVoiceObject struct {
	ID          string
	Name        string
	Type        string
	ExtraParams map[string]json.RawMessage
}
type RealtimeVoice struct {
	VoiceStr    *string
	VoiceObject *RealtimeVoiceObject
}

func (v RealtimeVoice) MarshalJSON() ([]byte, error) {
	if v.VoiceStr != nil && v.VoiceObject != nil {
		return nil, fmt.Errorf("both voice string and voice object are set; only one should be non-nil")
	}
	if v.VoiceStr != nil {
		return MarshalSorted(*v.VoiceStr)
	}
	if v.VoiceObject != nil {
		return MarshalSorted(v.VoiceObject)
	}
	return MarshalSorted(nil)
}

func (v *RealtimeVoice) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	v.VoiceStr, v.VoiceObject = nil, nil
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] == '"' {

		if len(trimmed) >= 2 && trimmed[len(trimmed)-1] == '"' && bytes.IndexByte(trimmed, '\\') < 0 {
			name := string(trimmed[1 : len(trimmed)-1])
			v.VoiceStr = &name
			return nil
		}
		var name string
		if err := Unmarshal(trimmed, &name); err != nil {
			return err
		}
		v.VoiceStr = &name
		return nil
	}
	var object RealtimeVoiceObject
	if err := Unmarshal(trimmed, &object); err != nil {
		return err
	}
	v.VoiceObject = &object
	return nil
}

func (v *RealtimeVoice) GetName() string {
	switch {
	case v == nil:
		return ""
	case v.VoiceStr != nil:
		return *v.VoiceStr
	case v.VoiceObject != nil && v.VoiceObject.Name != "":
		return v.VoiceObject.Name
	case v.VoiceObject != nil:
		return v.VoiceObject.ID
	}
	return ""
}

func (o RealtimeVoiceObject) MarshalJSON() ([]byte, error) {
	out := make(map[string]json.RawMessage, len(o.ExtraParams)+3)
	maps.Copy(out, o.ExtraParams)
	for _, field := range [...]struct{ key, value string }{{"id", o.ID}, {"name", o.Name}, {"type", o.Type}} {
		if field.value == "" {
			continue
		}
		encoded, err := MarshalSorted(field.value)
		if err != nil {
			return nil, err
		}
		out[field.key] = encoded
	}
	return MarshalSorted(out)
}

func (o *RealtimeVoiceObject) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := Unmarshal(data, &fields); err != nil {
		return err
	}
	*o = RealtimeVoiceObject{}
	for _, field := range [...]struct {
		key    string
		target *string
	}{{"id", &o.ID}, {"name", &o.Name}, {"type", &o.Type}} {
		raw, ok := fields[field.key]
		if !ok || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)) {
			continue
		}
		if err := Unmarshal(raw, field.target); err != nil {
			return fmt.Errorf("voice %s: %w", field.key, err)
		}
		delete(fields, field.key)
	}
	if len(fields) > 0 {
		o.ExtraParams = fields
	}
	return nil
}
