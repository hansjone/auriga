// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Duration time.Duration

func (d Duration) String() string {
	return time.Duration(d).String()
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	dur, err := ParseFlexibleDuration(data, "duration")
	if err != nil {
		return err
	}
	*d = Duration(dur)
	return nil
}

func ParseFlexibleDuration(data json.RawMessage, fieldName string) (time.Duration, error) {
	if len(data) == 0 || string(data) == "null" {
		return 0, nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return 0, err
		}

		if strings.TrimSpace(s) == "" {
			return 0, nil
		}
		dur, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("invalid %s %q: use a Go duration string like \"5s\", \"500ms\", \"1m30s\"", fieldName, s)
		}
		return dur, nil
	}

	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		return 0, fmt.Errorf("invalid %s: expected a duration string (e.g. \"5s\") or integer nanoseconds: %w", fieldName, err)
	}
	return time.Duration(n), nil
}
