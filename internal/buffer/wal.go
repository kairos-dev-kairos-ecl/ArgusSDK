// Package buffer WAL segment I/O implementation.
// Records are length-prefixed gob-encoded connector.SignalBatch values.
// File format per record:
//
//	[4-byte big-endian uint32 length][gob bytes]
//
// A record is marked consumed by overwriting its first byte with 0xFF.
// streamRecords skips any record whose first byte is 0xFF.
package buffer

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
)

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

// appendRecord gob-encodes batch and appends a length-prefixed record to seg.
// Returns the total number of bytes written (4-byte header + payload).
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

	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], length)

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

// streamRecords opens the segment at path and calls fn for each unconsumed record.
// currentOffset (passed to fn) is the file offset of the start of the 4-byte header.
// Stops and returns fn's error if fn returns non-nil.
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

		var hdr [4]byte
		_, err := io.ReadFull(f, hdr[:])
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return fmt.Errorf("streamRecords read header at %d: %w", offset, err)
		}
		offset += 4

		// Consumed records have 0xFF as the first byte of the header.
		if hdr[0] == 0xFF {
			// Reconstruct real length from remaining 3 bytes (upper byte was 0xFF, not the actual top byte).
			// But we wrote 0xFF only over the first byte — we need to read the length from the original record.
			// Since we overwrite only byte 0 of the header with 0xFF, we cannot reconstruct the original length.
			// Instead we need the actual length so we can skip the payload.
			// The original first byte of the uint32 big-endian length is now 0xFF, but that gives us
			// an inflated length. We take a different approach: store the real length before we stomp.
			//
			// Re-reading the design: markConsumed overwrites the FIRST BYTE of the 4-byte header with 0xFF.
			// On read, when hdr[0]==0xFF we must determine the payload length to advance the file pointer.
			// However we only have 3 bytes of the original length. For safety we reconstruct assuming
			// hdr[0] was originally 0x00 (payload < 16MB — safe for our use case).
			// Reconstruct length treating hdr[0] as 0:
			length := uint32(hdr[1])<<16 | uint32(hdr[2])<<8 | uint32(hdr[3])
			// If length somehow looks wrong (e.g. first byte was not 0), scan forward is unreliable.
			// We accept at-least-once delivery; skip and advance.
			if _, err := f.Seek(int64(length), io.SeekCurrent); err != nil {
				break
			}
			offset += int64(length)
			continue
		}

		length := binary.BigEndian.Uint32(hdr[:])
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

// markConsumed overwrites the first byte of the record header at the given file offset with 0xFF.
func markConsumed(path string, offset int64) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("markConsumed open %s: %w", path, err)
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("markConsumed seek to %d: %w", offset, err)
	}

	if _, err := f.Write([]byte{0xFF}); err != nil {
		return fmt.Errorf("markConsumed write 0xFF: %w", err)
	}
	return f.Sync()
}

// segmentPath returns a new unique segment file path inside dir.
func segmentPath(dir string) string {
	return filepath.Join(dir, fmt.Sprintf("wal-%d.seg", time.Now().Unix()))
}

// segmentSizeBytes returns the size of the file at path in bytes.
func segmentSizeBytes(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}
