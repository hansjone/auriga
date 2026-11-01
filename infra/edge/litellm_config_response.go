package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
)

const liteLLMUIConfigPath = "/ai/.well-known/litellm-ui-config"

// Only this public UI configuration field follows the authenticated entry's
// presentation origin. Do not rewrite API responses or arbitrary response text.
func rewriteLiteLLMUIConfig(resp *http.Response, origin string) error {
	rejected := fmt.Errorf("invalid LiteLLM UI configuration")
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "" || origin == "" {
		return rejected
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIBodyBytes+1))
	if err != nil || len(raw) > maxAPIBodyBytes {
		return rejected
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return rejected
	}
	config := map[string]json.RawMessage{}
	for decoder.More() {
		token, err = decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || config[name] != nil {
			return rejected
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return rejected
		}
		config[name] = value
	}
	if _, err = decoder.Token(); err != nil || decoder.Decode(new(any)) != io.EOF {
		return rejected
	}
	value, exists := config["proxy_base_url"]
	var current *string
	if !exists || json.Unmarshal(value, &current) != nil {
		return rejected
	}
	config["proxy_base_url"], _ = json.Marshal(origin + "/ai")
	body, err := json.Marshal(config)
	if err != nil {
		return rejected
	}
	if err = resp.Body.Close(); err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.Header.Set("Cache-Control", "no-store")
	resp.Header.Del("ETag")
	resp.Header.Del("Content-MD5")
	return nil
}
