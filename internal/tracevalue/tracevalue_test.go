package tracevalue

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMarshalRedactsBeforeTruncation(t *testing.T) {
	const limit = 1024
	result := Marshal(map[string]any{
		"authorization": "Bearer private-secret",
		"image":         "data:image/png;base64,c2VjcmV0",
		"padding":       strings.Repeat("x", limit*2),
	}, limit)
	if strings.Contains(result, "private-secret") || strings.Contains(result, "c2VjcmV0") {
		t.Fatal("trace still contains sensitive data")
	}
	if !strings.Contains(result, "redacted") || !strings.Contains(result, "image/png attachment data omitted") {
		t.Fatal("trace lost redaction markers")
	}
	if len(result) > limit || !strings.HasSuffix(result, "… [truncated]") || !utf8.ValidString(result) {
		t.Fatalf("trace is not safely bounded: bytes=%d valid=%v", len(result), utf8.ValidString(result))
	}
}

func TestSanitizePreservesNonJSONText(t *testing.T) {
	const input = "plain tool output"
	if result := Sanitize(input); result != input {
		t.Fatalf("Sanitize() = %q, want %q", result, input)
	}
}
