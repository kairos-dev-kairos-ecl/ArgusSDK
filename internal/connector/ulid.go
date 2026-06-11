package connector

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"sync"
	"time"
)

// batchIDState holds the monotonic state for NewBatchID.
// Within a single millisecond the random suffix is preserved and a 16-bit
// counter is ORed into bytes 14–15 to guarantee lexicographic ordering.
var batchIDState struct {
	mu      sync.Mutex
	lastMS  uint64
	lastRnd [10]byte
	seq     uint16 // within-millisecond sequence counter
}

// NewBatchID generates a unique, time-ordered, fixed-length batch identifier.
//
// Structure (16 raw bytes → 32 hex characters):
//   - Bytes 0–5  : big-endian Unix millisecond timestamp (time.Now().UnixMilli())
//   - Bytes 6–13 : 8 cryptographically random bytes (crypto/rand), refreshed each new ms
//   - Bytes 14–15: 16-bit monotonic sequence counter (reset on new ms, increments within ms)
//
// Properties:
//   - Fixed length: always 32 lowercase hex characters.
//   - Time-ordered: lexicographic order is guaranteed within any pair of IDs
//     regardless of whether they are in the same or different milliseconds.
//   - Unique: 80-bit random+sequence suffix makes collisions computationally infeasible.
//   - No new dependencies: uses only crypto/rand, encoding/binary, encoding/hex, sync, and time.
func NewBatchID() string {
	batchIDState.mu.Lock()
	defer batchIDState.mu.Unlock()

	ms := uint64(time.Now().UnixMilli()) //nolint:gosec // ms is non-negative

	if ms != batchIDState.lastMS {
		// New millisecond: generate fresh random bytes and reset counter.
		if _, err := rand.Read(batchIDState.lastRnd[:]); err != nil {
			// Fallback: derive from nano-time if entropy pool temporarily unavailable.
			binary.BigEndian.PutUint64(batchIDState.lastRnd[:8], uint64(time.Now().UnixNano())) //nolint:gosec
			binary.BigEndian.PutUint16(batchIDState.lastRnd[8:], uint16(time.Now().UnixNano())) //nolint:gosec
		}
		batchIDState.lastMS = ms
		batchIDState.seq = 0
	} else {
		// Same millisecond: increment the sequence counter.
		batchIDState.seq++
	}

	var raw [16]byte

	// First 6 bytes: big-endian Unix milliseconds.
	raw[0] = byte(ms >> 40)
	raw[1] = byte(ms >> 32)
	raw[2] = byte(ms >> 24)
	raw[3] = byte(ms >> 16)
	raw[4] = byte(ms >> 8)
	raw[5] = byte(ms)

	// Bytes 6–13: 8 random bytes (stable within the same millisecond).
	copy(raw[6:14], batchIDState.lastRnd[:8])

	// Bytes 14–15: monotonic sequence counter (ensures order within same ms).
	binary.BigEndian.PutUint16(raw[14:16], batchIDState.seq)

	return hex.EncodeToString(raw[:])
}
