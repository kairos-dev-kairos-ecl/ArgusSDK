// White-box tests for WAL segment I/O (package buffer — access to unexported types).
package buffer

import (
	"path/filepath"
	"testing"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
)

func makeSegPath(dir string) string {
	return filepath.Join(dir, "wal-test.seg")
}

// TestWAL_AppendReadRoundTrip verifies that a batch written with appendRecord
// is faithfully recovered by streamRecords.
func TestWAL_AppendReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := makeSegPath(dir)

	seg, err := openSegment(path)
	if err != nil {
		t.Fatalf("openSegment: %v", err)
	}

	batch := &connector.SignalBatch{BatchID: "test-1"}
	if _, err := appendRecord(seg, batch); err != nil {
		_ = seg.file.Close()
		t.Fatalf("appendRecord: %v", err)
	}
	// Close segment before reading on Windows (exclusive file access).
	_ = seg.file.Close()

	var collected []*connector.SignalBatch
	if err := streamRecords(path, func(_ int64, b *connector.SignalBatch) error {
		collected = append(collected, b)
		return nil
	}); err != nil {
		t.Fatalf("streamRecords: %v", err)
	}

	if len(collected) != 1 {
		t.Fatalf("expected 1 record, got %d", len(collected))
	}
	if collected[0].BatchID != "test-1" {
		t.Errorf("BatchID mismatch: got %q, want %q", collected[0].BatchID, "test-1")
	}
}

// TestWAL_MarkConsumed verifies that marking a record consumed causes streamRecords
// to skip it.
func TestWAL_MarkConsumed(t *testing.T) {
	dir := t.TempDir()
	path := makeSegPath(dir)

	seg, err := openSegment(path)
	if err != nil {
		t.Fatalf("openSegment: %v", err)
	}

	batch := &connector.SignalBatch{BatchID: "to-consume"}
	if _, err := appendRecord(seg, batch); err != nil {
		_ = seg.file.Close()
		t.Fatalf("appendRecord: %v", err)
	}
	// Close segment before marking/reading on Windows (exclusive file access).
	_ = seg.file.Close()

	// Mark record at offset 0 as consumed.
	if err := markConsumed(path, 0); err != nil {
		t.Fatalf("markConsumed: %v", err)
	}

	called := 0
	if err := streamRecords(path, func(_ int64, _ *connector.SignalBatch) error {
		called++
		return nil
	}); err != nil {
		t.Fatalf("streamRecords after markConsumed: %v", err)
	}

	if called != 0 {
		t.Errorf("expected fn NOT to be called after markConsumed, but called %d times", called)
	}
}
