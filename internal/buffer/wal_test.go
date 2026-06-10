// White-box tests for WAL format and segment naming (package buffer — access to unexported types).
package buffer

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// TestWAL_StatusByteFormat (F1): verifies the new record format is
// [1-byte status][4-byte BE length][payload]. Status 0x00 = live.
func TestWAL_StatusByteFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.seg")
	seg, err := openSegment(path)
	if err != nil {
		t.Fatalf("openSegment: %v", err)
	}

	batch := &connector.SignalBatch{BatchID: "status-byte-test"}
	n, err := appendRecord(seg, batch)
	if err != nil {
		_ = seg.file.Close()
		t.Fatalf("appendRecord: %v", err)
	}
	if err := seg.file.Close(); err != nil {
		t.Fatalf("close segment: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Byte 0 must be the status byte: 0x00 (live).
	if raw[0] != 0x00 {
		t.Errorf("status byte: got 0x%02X, want 0x00 — record format must be [status][len4][payload]", raw[0])
	}

	// Bytes 1..4 are the big-endian uint32 payload length.
	payloadLen := binary.BigEndian.Uint32(raw[1:5])

	// Total file size must equal 5 (header) + payloadLen.
	expectedSize := 5 + int(payloadLen)
	if len(raw) != expectedSize {
		t.Errorf("file size: got %d, want %d (5+%d) — expected [status:1][len:4][payload] format", len(raw), expectedSize, payloadLen)
	}

	// Written bytes from appendRecord must equal total file size (5 + payload).
	if n != expectedSize {
		t.Errorf("appendRecord returned %d bytes written, want %d (5+payload)", n, expectedSize)
	}
}

// TestWAL_MarkConsumedSkips (F1): markConsumed writes 0xFF to the status byte
// only; length bytes are untouched; streamRecords skips the consumed record and
// still yields the second record with the correct BatchID.
func TestWAL_MarkConsumedSkips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skip.seg")
	seg, err := openSegment(path)
	if err != nil {
		t.Fatalf("openSegment: %v", err)
	}

	batchA := &connector.SignalBatch{BatchID: "A"}
	batchB := &connector.SignalBatch{BatchID: "B"}

	if _, err := appendRecord(seg, batchA); err != nil {
		_ = seg.file.Close()
		t.Fatalf("appendRecord A: %v", err)
	}
	if _, err := appendRecord(seg, batchB); err != nil {
		_ = seg.file.Close()
		t.Fatalf("appendRecord B: %v", err)
	}
	if err := seg.file.Close(); err != nil {
		t.Fatalf("close segment: %v", err)
	}

	// Record A starts at offset 0.
	const offsetA int64 = 0

	// Mark A consumed.
	if err := markConsumed(path, offsetA); err != nil {
		t.Fatalf("markConsumed: %v", err)
	}

	// Read raw file — verify status byte is 0xFF and length bytes (1..4) are intact.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if raw[offsetA] != 0xFF {
		t.Errorf("status byte after markConsumed: got 0x%02X, want 0xFF", raw[offsetA])
	}
	// Under the OLD format, markConsumed stomped byte 0 of the 4-byte length prefix.
	// Under the NEW format, the length lives at bytes 1..4 and must not be all 0xFF.
	if raw[1] == 0xFF && raw[2] == 0xFF && raw[3] == 0xFF && raw[4] == 0xFF {
		t.Error("length bytes are all 0xFF — markConsumed stomped the length prefix (F1 not fixed)")
	}

	// streamRecords must yield only B.
	var seen []string
	if err := streamRecords(path, func(_ int64, b *connector.SignalBatch) error {
		seen = append(seen, b.BatchID)
		return nil
	}); err != nil {
		t.Fatalf("streamRecords: %v", err)
	}
	if len(seen) != 1 || seen[0] != "B" {
		t.Errorf("expected only [B], got %v", seen)
	}
}

// TestWAL_LargePayloadOffsetIntegrity (F1, 16 MB regression): writes a raw
// record with payload length >= 16,777,216 as the first record, followed by a
// valid second record. After markConsumed, streamRecords must use the intact
// 4-byte length to skip (no 3-byte reconstruction heuristic) and decode the
// second record correctly.
func TestWAL_LargePayloadOffsetIntegrity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.seg")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatalf("create file: %v", err)
	}

	// Construct a raw large record: [0x00][4-byte BE length = 16,777,216][payload]
	// 16,777,216 = 0x01_00_00_00 — the top byte is non-zero, so the old 3-byte
	// reconstruction (assuming top byte == 0x00) would produce wrong length.
	const largeLen uint32 = 16_777_216
	var hdr [5]byte
	hdr[0] = 0x00 // status: live
	binary.BigEndian.PutUint32(hdr[1:], largeLen)
	if _, err := f.Write(hdr[:]); err != nil {
		_ = f.Close()
		t.Fatalf("write large hdr: %v", err)
	}
	// Write largeLen zero bytes as payload (not gob-decodable — intentional;
	// this exercises the consumed-skip path, not the decode path).
	payload := make([]byte, largeLen)
	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		t.Fatalf("write large payload: %v", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		t.Fatalf("sync: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close after large write: %v", err)
	}

	// Append a valid second record via the WAL API.
	seg, err := openSegment(path)
	if err != nil {
		t.Fatalf("openSegment: %v", err)
	}
	batchGood := &connector.SignalBatch{BatchID: "after-large"}
	if _, err := appendRecord(seg, batchGood); err != nil {
		_ = seg.file.Close()
		t.Fatalf("appendRecord good: %v", err)
	}
	if err := seg.file.Close(); err != nil {
		t.Fatalf("close seg: %v", err)
	}

	// Mark the large record (offset 0) consumed.
	if err := markConsumed(path, 0); err != nil {
		t.Fatalf("markConsumed large: %v", err)
	}

	// streamRecords must skip the large consumed record using the intact 4-byte
	// length and yield only "after-large".
	var seen []string
	if err := streamRecords(path, func(_ int64, b *connector.SignalBatch) error {
		seen = append(seen, b.BatchID)
		return nil
	}); err != nil {
		t.Fatalf("streamRecords after large consumed: %v", err)
	}
	if len(seen) != 1 || seen[0] != "after-large" {
		t.Errorf("expected [after-large], got %v — offset desync indicates 3-byte reconstruction heuristic still present", seen)
	}
}

// TestWAL_SegmentNaming (F3): two calls to segmentPath in the same second must
// produce different paths (seq suffix). parseSegmentName round-trips (unix, seq).
func TestWAL_SegmentNaming(t *testing.T) {
	dir := t.TempDir()

	p1 := segmentPath(dir)
	p2 := segmentPath(dir)

	if p1 == p2 {
		t.Errorf("segmentPath produced identical paths in same second: %q — seq suffix missing", p1)
	}

	name1 := filepath.Base(p1)
	name2 := filepath.Base(p2)

	unix1, seq1, ok1 := parseSegmentName(name1)
	unix2, seq2, ok2 := parseSegmentName(name2)

	if !ok1 {
		t.Errorf("parseSegmentName(%q) returned ok=false", name1)
	}
	if !ok2 {
		t.Errorf("parseSegmentName(%q) returned ok=false", name2)
	}

	// Unix timestamps must be sane.
	now := time.Now().Unix()
	for _, u := range []int64{unix1, unix2} {
		if u <= 0 || u > now+5 {
			t.Errorf("unix timestamp out of range: %d (now=%d)", u, now)
		}
	}

	// Seq numbers must differ between two consecutive calls.
	if seq1 == seq2 {
		t.Errorf("seq suffix not unique: seq1=%d seq2=%d", seq1, seq2)
	}
}

// TestWAL_ListSegments (F3): listSegments returns only wal-<unix>-<seq>.seg
// files sorted oldest-first by (unix, seq); other files are ignored.
func TestWAL_ListSegments(t *testing.T) {
	dir := t.TempDir()

	// Create files in shuffled order to confirm sort.
	names := []string{
		"wal-1000-2.seg",
		"wal-999-1.seg",
		"wal-1000-1.seg",
		"other-file.txt",
		"wal-invalid.seg", // no seq — should be excluded
	}
	for _, n := range names {
		f, err := os.Create(filepath.Join(dir, n))
		if err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
		f.Close()
	}

	refs, err := listSegments(dir)
	if err != nil {
		t.Fatalf("listSegments: %v", err)
	}

	want := []string{"wal-999-1.seg", "wal-1000-1.seg", "wal-1000-2.seg"}
	if len(refs) != len(want) {
		t.Fatalf("listSegments returned %d refs, want %d: %v", len(refs), len(want), refs)
	}
	for i, ref := range refs {
		if filepath.Base(ref.path) != want[i] {
			t.Errorf("refs[%d] = %q, want %q", i, filepath.Base(ref.path), want[i])
		}
	}
}

// TestWAL_CountLiveRecords verifies countLiveRecords returns the correct count
// of unconsumed records in a segment.
func TestWAL_CountLiveRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "count.seg")
	seg, err := openSegment(path)
	if err != nil {
		t.Fatalf("openSegment: %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := appendRecord(seg, &connector.SignalBatch{BatchID: "x"}); err != nil {
			_ = seg.file.Close()
			t.Fatalf("appendRecord %d: %v", i, err)
		}
	}
	if err := seg.file.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Mark first record consumed (offset 0).
	if err := markConsumed(path, 0); err != nil {
		t.Fatalf("markConsumed: %v", err)
	}

	n, err := countLiveRecords(path)
	if err != nil {
		t.Fatalf("countLiveRecords: %v", err)
	}
	// 3 written, 1 consumed → 2 live.
	if n != 2 {
		t.Errorf("countLiveRecords: got %d, want 2", n)
	}
}

// suppress unused import in partial builds
var _ = io.EOF
