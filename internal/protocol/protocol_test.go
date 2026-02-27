package protocol_test

import (
	"testing"

	"github.com/tiroq/peeksy/internal/chunker"
	"github.com/tiroq/peeksy/internal/protocol"
)

func TestEncodeDecode_roundtrip(t *testing.T) {
	original := chunker.Chunk{Index: 3, Total: 10, Data: "SGVsbG8gV29ybGQ="}

	encoded, err := protocol.Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded, err := protocol.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if decoded.Index != original.Index || decoded.Total != original.Total || decoded.Data != original.Data {
		t.Errorf("round-trip mismatch: got %+v, want %+v", decoded, original)
	}
}

func TestDecode_invalidJSON(t *testing.T) {
	_, err := protocol.Decode("not-json")
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestDecode_missingFields(t *testing.T) {
	cases := []string{
		`{"c":0,"t":5,"d":"abc"}`,  // c == 0
		`{"c":1,"t":0,"d":"abc"}`,  // t == 0
		`{"c":1,"t":5,"d":""}`,     // d empty
	}
	for _, tc := range cases {
		_, err := protocol.Decode(tc)
		if err == nil {
			t.Errorf("expected error for %s, got nil", tc)
		}
	}
}

func TestEncode_json_format(t *testing.T) {
	c := chunker.Chunk{Index: 2, Total: 12, Data: "KJNiuefEF=="}
	s, err := protocol.Encode(c)
	if err != nil {
		t.Fatal(err)
	}
	// Verify the key names match the documented format {c, t, d}
	want := `{"c":2,"t":12,"d":"KJNiuefEF=="}`
	if s != want {
		t.Errorf("got %s, want %s", s, want)
	}
}
