// Package chunker provides utilities for splitting a file into fixed-size
// chunks and reassembling those chunks back into the original file.
package chunker

import (
	"encoding/base64"
	"fmt"
	"os"
)

// DefaultChunkSize is the number of raw bytes per chunk.
// 800 bytes → ~1 067 base64 chars, which fits comfortably in a QR code.
const DefaultChunkSize = 800

// Chunk represents one piece of a file.
type Chunk struct {
	Index int    // 1-based position of this chunk
	Total int    // total number of chunks for the file
	Data  string // base64-encoded raw bytes for this chunk
}

// SplitFile reads the file at path and returns a slice of Chunks.
// chunkSize ≤ 0 falls back to DefaultChunkSize.
func SplitFile(path string, chunkSize int) ([]Chunk, error) {
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("file is empty")
	}

	total := (len(raw) + chunkSize - 1) / chunkSize
	chunks := make([]Chunk, 0, total)
	for i := 0; i < total; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(raw) {
			end = len(raw)
		}
		chunks = append(chunks, Chunk{
			Index: i + 1,
			Total: total,
			Data:  base64.StdEncoding.EncodeToString(raw[start:end]),
		})
	}
	return chunks, nil
}

// Assemble reconstructs file bytes from a (possibly out-of-order) slice of
// Chunks. It returns an error if any chunk index in the range [1, total] is
// absent.
func Assemble(chunks []Chunk) ([]byte, error) {
	if len(chunks) == 0 {
		return nil, fmt.Errorf("no chunks provided")
	}
	total := chunks[0].Total
	chunkMap := make(map[int][]byte, total)
	for _, c := range chunks {
		raw, err := base64.StdEncoding.DecodeString(c.Data)
		if err != nil {
			return nil, fmt.Errorf("decoding chunk %d: %w", c.Index, err)
		}
		chunkMap[c.Index] = raw
	}
	for i := 1; i <= total; i++ {
		if _, ok := chunkMap[i]; !ok {
			return nil, fmt.Errorf("missing chunk %d of %d", i, total)
		}
	}
	var result []byte
	for i := 1; i <= total; i++ {
		result = append(result, chunkMap[i]...)
	}
	return result, nil
}

// MissingIndices returns the chunk indices (in the range [1, total]) that are
// not present in chunks.
func MissingIndices(chunks []Chunk, total int) []int {
	present := make(map[int]bool, len(chunks))
	for _, c := range chunks {
		present[c.Index] = true
	}
	var missing []int
	for i := 1; i <= total; i++ {
		if !present[i] {
			missing = append(missing, i)
		}
	}
	return missing
}
