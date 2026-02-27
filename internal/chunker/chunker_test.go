package chunker_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/tiroq/peeksy/internal/chunker"
)

func TestSplitFileAndAssemble_roundtrip(t *testing.T) {
	// Create a temp file with known content.
	content := make([]byte, 2500) // spans multiple default chunks
	for i := range content {
		content[i] = byte(i % 256)
	}
	tmp := filepath.Join(t.TempDir(), "test.bin")
	if err := os.WriteFile(tmp, content, 0600); err != nil {
		t.Fatal(err)
	}

	chunks, err := chunker.SplitFile(tmp, 0) // 0 → DefaultChunkSize
	if err != nil {
		t.Fatalf("SplitFile: %v", err)
	}
	// 2500 bytes / 800 bytes per chunk = 4 chunks (last chunk has 100 bytes)
	expectedTotal := (2500 + chunker.DefaultChunkSize - 1) / chunker.DefaultChunkSize
	if len(chunks) != expectedTotal {
		t.Fatalf("want %d chunks, got %d", expectedTotal, len(chunks))
	}
	for i, c := range chunks {
		if c.Index != i+1 {
			t.Errorf("chunk[%d].Index = %d, want %d", i, c.Index, i+1)
		}
		if c.Total != expectedTotal {
			t.Errorf("chunk[%d].Total = %d, want %d", i, c.Total, expectedTotal)
		}
		if c.Data == "" {
			t.Errorf("chunk[%d].Data is empty", i)
		}
	}

	got, err := chunker.Assemble(chunks)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if string(got) != string(content) {
		t.Error("round-trip content mismatch")
	}
}

func TestSplitFile_customChunkSize(t *testing.T) {
	content := []byte("hello world, this is a test")
	tmp := filepath.Join(t.TempDir(), "small.txt")
	if err := os.WriteFile(tmp, content, 0600); err != nil {
		t.Fatal(err)
	}

	chunks, err := chunker.SplitFile(tmp, 10)
	if err != nil {
		t.Fatalf("SplitFile: %v", err)
	}
	expected := (len(content) + 9) / 10
	if len(chunks) != expected {
		t.Fatalf("want %d chunks, got %d", expected, len(chunks))
	}

	got, err := chunker.Assemble(chunks)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("got %q, want %q", got, content)
	}
}

func TestSplitFile_emptyFile(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "empty.bin")
	if err := os.WriteFile(tmp, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := chunker.SplitFile(tmp, 0)
	if err == nil {
		t.Error("expected error for empty file, got nil")
	}
}

func TestSplitFile_missingFile(t *testing.T) {
	_, err := chunker.SplitFile("/does/not/exist.bin", 0)
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestAssemble_missingChunk(t *testing.T) {
	chunks := []chunker.Chunk{
		{Index: 1, Total: 2, Data: base64.StdEncoding.EncodeToString([]byte("hello"))},
		// chunk 2 is intentionally absent
	}
	_, err := chunker.Assemble(chunks)
	if err == nil {
		t.Error("expected error when chunk is missing, got nil")
	}
}

func TestAssemble_outOfOrder(t *testing.T) {
	data := []byte("abcdefghij")
	chunks := []chunker.Chunk{
		{Index: 2, Total: 2, Data: base64.StdEncoding.EncodeToString(data[5:])},
		{Index: 1, Total: 2, Data: base64.StdEncoding.EncodeToString(data[:5])},
	}
	got, err := chunker.Assemble(chunks)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}
}

func TestMissingIndices(t *testing.T) {
	chunks := []chunker.Chunk{
		{Index: 1, Total: 4},
		{Index: 3, Total: 4},
	}
	missing := chunker.MissingIndices(chunks, 4)
	if len(missing) != 2 || missing[0] != 2 || missing[1] != 4 {
		t.Errorf("MissingIndices = %v, want [2 4]", missing)
	}
}
