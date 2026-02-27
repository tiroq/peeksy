// Package protocol defines the data format used in Peeksy QR codes and
// provides helpers to encode/decode it.
//
// Each QR code carries a compact JSON payload:
//
//	{"c":2,"t":10,"d":"KJNiuefEF=="}
//
// where:
//   - c = current chunk index (1-based)
//   - t = total number of chunks
//   - d = base64-encoded bytes for this chunk
package protocol

import (
	"encoding/json"
	"fmt"

	"github.com/tiroq/peeksy/internal/chunker"
)

// Packet is the JSON structure stored inside every QR code.
// Field names are intentionally single-character to minimise QR payload size.
type Packet struct {
	C int    `json:"c"` // current chunk index (1-based)
	T int    `json:"t"` // total chunk count
	D string `json:"d"` // base64-encoded chunk bytes
}

// Encode serialises a Chunk into the compact JSON string that gets embedded
// in a QR code.
func Encode(c chunker.Chunk) (string, error) {
	p := Packet{C: c.Index, T: c.Total, D: c.Data}
	b, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("encoding packet: %w", err)
	}
	return string(b), nil
}

// Decode parses a QR code payload string back into a Chunk.
func Decode(s string) (chunker.Chunk, error) {
	var p Packet
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return chunker.Chunk{}, fmt.Errorf("decoding packet: %w", err)
	}
	if p.C <= 0 || p.T <= 0 || p.D == "" {
		return chunker.Chunk{}, fmt.Errorf("invalid packet fields: c=%d t=%d d=%q", p.C, p.T, p.D)
	}
	return chunker.Chunk{Index: p.C, Total: p.T, Data: p.D}, nil
}
