// Package buffer WAL segment I/O implementation.
//
// # Record format
//
// Each record in a WAL segment file is laid out as:
//
//	[1-byte status][4-byte big-endian uint32 length][gob-encoded SignalBatch]
//
// Status values:
//   - 0x00 — live (not yet delivered)
//   - 0xFF — consumed (successfully delivered; will be skipped on read)
//
// markConsumed writes ONLY the status byte (offset = record start).
// The 4-byte length field is always preserved intact, so the skip path in
// streamRecords never needs to reconstruct the length from partial bytes.
// This fixes F1: with the old 4-byte-only header, overwriting byte 0 with 0xFF
// corrupted payloads >= 16 MB (top length byte was assumed to be 0x00).
//
// # Segment naming
//
// Files are named wal-<unix>-<seq>.seg where:
//   - <unix> is the Unix timestamp at creation time (seconds).
//   - <seq>  is a monotonically increasing counter scoped to the process lifetime.
//
// The (unix, seq) pair sorts segments oldest-first even if two are created within
// the same second, fixing the name-collision bug in the old "wal-<unix>.seg" scheme.
package buffer

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
)

// Record format constants.
const (
	statusLive     byte = 0x00
	statusConsumed byte = 0xFF
	headerSize          = 5 // 1 status + 4 length
)

// segSeq is a process-lifetime monotonically increasing counter for segment naming.
var segSeq atomic.Uint64

// walSegment represents an open WAL segment file.
type walSegment struct {
	path string
	file *os.File
	mu   sync.Mutex
}

// openSegment opens or creates the segment file at path.
func openSegment(path string) (*walSegment, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("openSegment %s: %w", path, err)
	}
	return &walSegment{path: path, file: f}, nil
}

// appendRecord gob-encodes batch and appends a record to seg.
// Record format: [status:0x00][4-byte BE uint32 length][gob payload].
// Returns the total number of bytes written (5-byte header + payload).
func appendRecord(seg *walSegment, batch *connector.SignalBatch) (int, error) {
	seg.mu.Lock()
	defer seg.mu.Unlock()

	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(batch); err != nil {
		return 0, fmt.Errorf("appendRecord gob encode: %w", err)
	}

	payload := buf.Bytes()
	length := uint32(len(payload))

	var hdr [headerSize]byte
	hdr[0] = statusLive
	binary.BigEndian.PutUint32(hdr[1:], length)

	n1, err := seg.file.Write(hdr[:])
	if err != nil {
		return n1, fmt.Errorf("appendRecord write header: %w", err)
	}

	n2, err := seg.file.Write(payload)
	if err != nil {
		return n1 + n2, fmt.Errorf("appendRecord write payload: %w", err)
	}

	if err := seg.file.Sync(); err != nil {
		return n1 + n2, fmt.Errorf("appendRecord sync: %w", err)
	}

	return n1 + n2, nil
}

// streamRecords opens the segment at path and calls fn for each live (unconsumed) record.
// The offset passed to fn is the file offset of the start of the record's 5-byte header.
// Consumed records (status 0xFF) are skipped using the intact 4-byte length field.
// Stops and returns fn's error if fn returns non-nil.
// The file handle is always closed before this function returns.
func streamRecords(path string, fn func(offset int64, batch *connector.SignalBatch) error) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("streamRecords open %s: %w", path, err)
	}
	defer f.Close()

	var offset int64
	for {
		recordStart := offset

		var hdr [headerSize]byte
		_, err := io.ReadFull(f, hdr[:])
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return fmt.Errorf("streamRecords read header at %d: %w", offset, err)
		}
		offset += headerSize

		status := hdr[0]
		length := binary.BigEndian.Uint32(hdr[1:])

		if status == statusConsumed {
			// Skip payload using the intact 4-byte length (F1 fix: no reconstruction).
			if _, err := f.Seek(int64(length), io.SeekCurrent); err != nil {
				break
			}
			offset += int64(length)
			continue
		}

		if length == 0 {
			continue
		}

		payload := make([]byte, length)
		if _, err := io.ReadFull(f, payload); err != nil {
			break
		}
		offset += int64(length)

		var batch connector.SignalBatch
		dec := gob.NewDecoder(bytes.NewReader(payload))
		if err := dec.Decode(&batch); err != nil {
			// Corrupted record — skip.
			continue
		}

		if err := fn(recordStart, &batch); err != nil {
			return err
		}
	}
	return nil
}

// countLiveRecords returns the number of unconsumed records in the segment at path.
// The file handle is closed before this function returns.
func countLiveRecords(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("countLiveRecords open %s: %w", path, err)
	}
	defer f.Close()

	count := 0
	for {
		var hdr [headerSize]byte
		_, err := io.ReadFull(f, hdr[:])
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			break
		}
		status := hdr[0]
		length := binary.BigEndian.Uint32(hdr[1:])
		if _, err := f.Seek(int64(length), io.SeekCurrent); err != nil {
			break
		}
		if status == statusLive {
			count++
		}
	}
	return count, nil
}

// markConsumed writes the single status byte 0xFF at the record's file offset.
// It never touches the length bytes (bytes 1..4 of the header).
func markConsumed(path string, offset int64) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("markConsumed open %s: %w", path, err)
	}
	defer f.Close()

	return markConsumedAt(f, offset)
}

// markConsumedAt writes 0xFF at offset using an already-open file handle.
// It never touches the length bytes (bytes 1..4 of the header).
// Does NOT sync — caller is responsible for flushing if durability is needed.
func markConsumedAt(f *os.File, offset int64) error {
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("markConsumed seek to %d: %w", offset, err)
	}

	if _, err := f.Write([]byte{statusConsumed}); err != nil {
		return fmt.Errorf("markConsumed write 0xFF: %w", err)
	}
	return nil
}

// segmentPath returns a new unique segment file path inside dir.
// Format: wal-<unix>-<seq>.seg — never collides even within the same second.
func segmentPath(dir string) string {
	seq := segSeq.Add(1)
	return filepath.Join(dir, fmt.Sprintf("wal-%d-%d.seg", time.Now().Unix(), seq))
}

// segmentRef holds a parsed segment reference returned by listSegments.
type segmentRef struct {
	path    string
	unixSec int64
	seq     uint64
}

// parseSegmentName parses a filename of the form "wal-<unix>-<seq>.seg".
// Returns (unix, seq, true) on success, (0, 0, false) if the name does not match.
func parseSegmentName(name string) (unixSec int64, seq uint64, ok bool) {
	// Expected: wal-<digits>-<digits>.seg
	if !strings.HasPrefix(name, "wal-") || !strings.HasSuffix(name, ".seg") {
		return 0, 0, false
	}
	inner := name[4 : len(name)-4] // strip "wal-" prefix and ".seg" suffix
	idx := strings.LastIndex(inner, "-")
	if idx < 0 {
		return 0, 0, false
	}
	unixStr := inner[:idx]
	seqStr := inner[idx+1:]
	u, err1 := strconv.ParseInt(unixStr, 10, 64)
	s, err2 := strconv.ParseUint(seqStr, 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return u, s, true
}

// listSegments returns all valid wal-<unix>-<seq>.seg files in dir, sorted
// oldest-first by (unixSec, seq) using numeric (not lexicographic) ordering.
func listSegments(dir string) ([]segmentRef, error) {
	entries, err := filepath.Glob(filepath.Join(dir, "wal-*.seg"))
	if err != nil {
		return nil, fmt.Errorf("listSegments glob: %w", err)
	}

	var refs []segmentRef
	for _, p := range entries {
		name := filepath.Base(p)
		u, s, ok := parseSegmentName(name)
		if !ok {
			continue
		}
		refs = append(refs, segmentRef{path: p, unixSec: u, seq: s})
	}

	sort.Slice(refs, func(i, j int) bool {
		if refs[i].unixSec != refs[j].unixSec {
			return refs[i].unixSec < refs[j].unixSec
		}
		return refs[i].seq < refs[j].seq
	})

	return refs, nil
}

// segmentSizeBytes returns the size of the file at path in bytes.
func segmentSizeBytes(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}
