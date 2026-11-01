// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import (
	"encoding/json"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type ChatResponseFormat struct {
	Type string

	raw json.RawMessage

	jsonSchema *OrderedMap
}

func (f *ChatResponseFormat) Name() (string, bool) {
	return f.stringField("name")
}

func (f *ChatResponseFormat) Description() (string, bool) {
	return f.stringField("description")
}

func (f *ChatResponseFormat) Strict() *bool {
	if f == nil {
		return nil
	}
	if len(f.raw) > 0 {
		if result := gjson.GetBytes(f.raw, "json_schema.strict"); result.Exists() && result.IsBool() {
			return Ptr(result.Bool())
		}
		return nil
	}
	if f.jsonSchema == nil {
		return nil
	}
	if value, ok := f.jsonSchema.Get("strict"); ok {
		if strict, ok := value.(bool); ok {
			return &strict
		}
	}
	return nil
}

func (f *ChatResponseFormat) HasJSONSchema() bool {
	if f == nil {
		return false
	}
	if len(f.raw) > 0 {
		return gjson.GetBytes(f.raw, "json_schema").IsObject()
	}
	return f.jsonSchema != nil
}

func (f *ChatResponseFormat) RawSchema() json.RawMessage {
	if f == nil {
		return nil
	}
	if len(f.raw) > 0 {
		if result := gjson.GetBytes(f.raw, "json_schema.schema"); result.Exists() {
			return json.RawMessage(result.Raw)
		}
		return nil
	}
	schema, ok := f.Schema()
	if !ok {
		return nil
	}
	encoded, err := MarshalSorted(schema)
	if err != nil {
		return nil
	}
	return encoded
}

func (f *ChatResponseFormat) Schema() (interface{}, bool) {
	if f == nil {
		return nil, false
	}
	if wrapper := f.wrapper(); wrapper != nil {
		return wrapper.Get("schema")
	}
	return nil, false
}

func (f *ChatResponseFormat) SchemaMap() (*OrderedMap, bool) {
	schema, ok := f.Schema()
	if !ok {
		return nil, false
	}
	return SafeExtractOrderedMap(schema)
}

func (f *ChatResponseFormat) wrapper() *OrderedMap {
	if f == nil {
		return nil
	}
	if f.jsonSchema == nil && len(f.raw) > 0 {
		result := gjson.GetBytes(f.raw, "json_schema")
		if !result.IsObject() {
			return nil
		}
		decoded := NewOrderedMap()
		if err := decoded.UnmarshalJSON([]byte(result.Raw)); err != nil {
			return nil
		}
		f.jsonSchema = decoded
	}
	return f.jsonSchema
}

func (f *ChatResponseFormat) stringField(key string) (string, bool) {
	if f == nil {
		return "", false
	}
	if len(f.raw) > 0 {
		result := gjson.GetBytes(f.raw, "json_schema."+key)
		if !result.Exists() || result.Type != gjson.String {
			return "", false
		}
		return result.String(), true
	}
	if f.jsonSchema == nil {
		return "", false
	}
	if value, ok := f.jsonSchema.Get(key); ok {
		if str, ok := SafeExtractString(value); ok {
			return str, true
		}
	}
	return "", false
}

func ParseChatResponseFormat(responseFormat *interface{}) (*ChatResponseFormat, bool) {
	if responseFormat == nil || *responseFormat == nil {
		return nil, false
	}

	if raw, ok := rawJSONBytes(*responseFormat); ok {
		result := gjson.GetBytes(raw, "type")
		if result.Type != gjson.String {
			return nil, false
		}
		return &ChatResponseFormat{Type: result.String(), raw: raw}, true
	}

	formatMap, ok := SafeExtractOrderedMap(*responseFormat)
	if !ok || formatMap == nil {
		return nil, false
	}
	typeRaw, ok := formatMap.Get("type")
	if !ok {
		return nil, false
	}
	formatType, ok := SafeExtractString(typeRaw)
	if !ok {
		return nil, false
	}

	parsed := &ChatResponseFormat{Type: formatType}
	if jsonSchemaRaw, ok := formatMap.Get("json_schema"); ok {
		if jsonSchema, ok := SafeExtractOrderedMap(jsonSchemaRaw); ok {
			parsed.jsonSchema = jsonSchema
		}
	}
	return parsed, true
}

func rawJSONBytes(value interface{}) (json.RawMessage, bool) {
	var raw []byte
	switch typed := value.(type) {
	case json.RawMessage:
		raw = typed
	case *json.RawMessage:
		if typed == nil {
			return nil, false
		}
		raw = *typed
	case []byte:
		raw = typed
	default:
		return nil, false
	}
	if !gjson.ParseBytes(raw).IsObject() {
		return nil, false
	}
	return raw, true
}

func (t *ResponsesTextConfig) GetFormat() *ResponsesTextConfigFormat {
	if t == nil {
		return nil
	}
	return t.Format
}

func ChatResponseFormatFromResponsesFormat(format *ResponsesTextConfigFormat) *interface{} {
	if format == nil {
		return nil
	}

	body, err := sjson.SetBytes([]byte("{}"), "type", format.Type)
	if err != nil {
		return nil
	}

	if format.Type == "json_schema" {

		wrapper := []byte("{}")
		if format.Name != nil {
			wrapper, _ = sjson.SetBytes(wrapper, "name", *format.Name)
		}
		if format.Description != nil {
			wrapper, _ = sjson.SetBytes(wrapper, "description", *format.Description)
		}
		if schema := format.JSONSchema.RawSchemaJSON(); len(schema) > 0 {
			wrapper, _ = sjson.SetRawBytes(wrapper, "schema", schema)
		}
		if format.Strict != nil {
			wrapper, _ = sjson.SetBytes(wrapper, "strict", *format.Strict)
		}
		body, _ = sjson.SetRawBytes(body, "json_schema", wrapper)
	}

	var responseFormat interface{} = json.RawMessage(body)
	return &responseFormat
}

func ResponsesTextConfigFormatFromChatResponseFormat(responseFormat *interface{}) *ResponsesTextConfigFormat {
	rf, ok := ParseChatResponseFormat(responseFormat)
	if !ok {
		return nil
	}

	format := &ResponsesTextConfigFormat{Type: rf.Type}
	if rf.Type != "json_schema" {
		return format
	}

	if !rf.HasJSONSchema() {
		return nil
	}
	if name, ok := rf.Name(); ok {
		format.Name = &name
	}
	if description, ok := rf.Description(); ok {
		format.Description = &description
	}
	format.Strict = rf.Strict()
	if schema, ok := rf.Schema(); ok {
		format.JSONSchema = JSONSchemaFromMap(schema)
	}
	return format
}
