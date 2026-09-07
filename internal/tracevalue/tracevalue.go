// Package tracevalue prepares structured runtime values for persistent traces.
package tracevalue

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Marshal redacts structured values before applying the storage limit. Redaction
// must happen first: truncating JSON can make it invalid and prevent safe parsing.
func Marshal(value any, limit int) string {
	var encoded string
	if text, ok := value.(string); ok {
		encoded = Sanitize(text)
	} else {
		data, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		encoded = Sanitize(string(data))
	}
	return truncateUTF8(encoded, limit)
}

// Sanitize preserves JSON structure while removing attachment data and common
// credential fields. Non-JSON text is returned unchanged.
func Sanitize(value string) string {
	var payload any
	if json.Unmarshal([]byte(value), &payload) != nil {
		return value
	}
	encoded, err := json.Marshal(sanitize(payload))
	if err != nil {
		return value
	}
	return string(encoded)
}

func sanitize(value any) any {
	switch typed := value.(type) {
	case string:
		if strings.HasPrefix(typed, "data:") {
			if marker := strings.Index(typed, ";base64,"); marker > len("data:") {
				return fmt.Sprintf("<%s attachment data omitted>", typed[len("data:"):marker])
			}
		}
		return typed
	case []any:
		for index := range typed {
			typed[index] = sanitize(typed[index])
		}
		return typed
	case map[string]any:
		for key := range typed {
			if isSensitiveKey(key) {
				typed[key] = "<redacted>"
				continue
			}
			typed[key] = sanitize(typed[key])
		}
		return typed
	default:
		return value
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(strings.TrimSpace(key)))
	for _, suffix := range []string{"apikey", "authorization", "password", "secret", "token", "cookie"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

func truncateUTF8(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	const suffix = "… [truncated]"
	if limit <= len(suffix) {
		cut := limit
		for cut > 0 && !utf8.ValidString(value[:cut]) {
			cut--
		}
		return value[:cut]
	}
	cut := limit - len(suffix)
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut] + suffix
}
