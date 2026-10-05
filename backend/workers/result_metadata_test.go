package workers

import (
	"errors"
	"testing"
)

// Certificate and http checks are the two that populate metadata, and they are
// exactly the ones whose results stopped reaching the index. Their payloads must
// decode into the object shape the mapping declares.
func TestDecodeResultMetadataAcceptsCheckPayloads(t *testing.T) {
	cases := map[string]string{
		"certificate": `{"expires_at":"2027-01-04T12:00:00Z"}`,
		"http":        `{"http_dns_ms":1.2,"http_connect_ms":3.4,"http_tls_ms":5.6,"http_server_ms":7.8,"http_redirects":0}`,
		"agent":       `{"ram_total_gb":16,"disk_total_gb":500}`,
		"empty":       `{}`,
	}

	for name, raw := range cases {
		decoded, err := decodeResultMetadata(raw)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", name, err)
			continue
		}
		if decoded == nil {
			t.Errorf("%s: decoded to nil", name)
		}
	}
}

func TestDecodeResultMetadataKeepsValues(t *testing.T) {
	decoded, err := decodeResultMetadata(`{"expires_at":"2027-01-04T12:00:00Z"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded["expires_at"] != "2027-01-04T12:00:00Z" {
		t.Fatalf("got %v, want the expiry timestamp", decoded["expires_at"])
	}
}

// Anything that is not an object would be rejected by the index just like the
// raw string was, so it has to be caught here rather than sent and lost.
func TestDecodeResultMetadataRejectsNonObjects(t *testing.T) {
	for name, raw := range map[string]string{
		"raw string":  `not json at all`,
		"json string": `"just a string"`,
		"array":       `[1,2,3]`,
		"number":      `42`,
		"null":        `null`,
	} {
		_, err := decodeResultMetadata(raw)
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		// Callers dispatch with errors.Is, so any rejection must carry the
		// sentinel — a bare *json.UnmarshalTypeError is invisible to them.
		if !errors.Is(err, ErrMetadataNotObject) {
			t.Errorf("%s: got untyped error %v", name, err)
		}
	}
}

func TestDecodeResultMetadataNullIsTyped(t *testing.T) {
	_, err := decodeResultMetadata(`null`)
	if !errors.Is(err, ErrMetadataNotObject) {
		t.Fatalf("got %v, want ErrMetadataNotObject", err)
	}
}
