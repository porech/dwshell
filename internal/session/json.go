package session

import "encoding/json"

// parseJSONObject decodes a JSON object, returning an empty map on failure.
func parseJSONObject(b []byte) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{}
	}
	return m
}
