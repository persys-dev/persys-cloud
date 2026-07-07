package runtime

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestDecodeComposeYAMLPayload_Base64(t *testing.T) {
	raw := "services:\n  web:\n    image: nginx:1.27\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))

	decoded, payloadType, err := decodeComposeYAMLPayload(encoded)
	if err != nil {
		t.Fatalf("decodeComposeYAMLPayload returned error: %v", err)
	}
	if payloadType != "base64" {
		t.Fatalf("expected payload type base64, got %q", payloadType)
	}
	if string(decoded) != raw {
		t.Fatalf("decoded payload mismatch: got %q", string(decoded))
	}
}

func TestDecodeComposeYAMLPayload_InlineYAML(t *testing.T) {
	raw := "version: \"3.8\"\nservices:\n  redis:\n    image: redis:7-alpine\n"

	decoded, payloadType, err := decodeComposeYAMLPayload(raw)
	if err != nil {
		t.Fatalf("decodeComposeYAMLPayload returned error: %v", err)
	}
	if payloadType != "inline" {
		t.Fatalf("expected payload type inline, got %q", payloadType)
	}
	if strings.TrimSpace(string(decoded)) != strings.TrimSpace(raw) {
		t.Fatalf("decoded payload mismatch: got %q", string(decoded))
	}
}

func TestDecodeComposeYAMLPayload_InvalidNonInline(t *testing.T) {
	if _, _, err := decodeComposeYAMLPayload("%%%not-valid-base64%%%"); err == nil {
		t.Fatalf("expected decodeComposeYAMLPayload to fail for invalid non-inline payload")
	}
}
