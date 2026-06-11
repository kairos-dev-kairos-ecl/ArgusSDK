package connector

import (
	"strings"
	"testing"
)

// TestNewBatchID verifies the four behavioral requirements:
// 1. Non-empty string on every call.
// 2. Two consecutive calls return different strings (uniqueness).
// 3. Fixed length (deterministic).
// 4. Time-ordered: an ID generated later sorts >= one generated earlier (lexicographic).
func TestNewBatchID(t *testing.T) {
	t.Run("non-empty", func(t *testing.T) {
		id := NewBatchID()
		if id == "" {
			t.Fatal("NewBatchID() returned empty string")
		}
	})

	t.Run("uniqueness", func(t *testing.T) {
		a := NewBatchID()
		b := NewBatchID()
		if a == b {
			t.Fatalf("NewBatchID() returned same value on consecutive calls: %s", a)
		}
	})

	t.Run("fixed_length", func(t *testing.T) {
		ids := make([]string, 10)
		for i := range ids {
			ids[i] = NewBatchID()
		}
		length := len(ids[0])
		for i, id := range ids {
			if len(id) != length {
				t.Fatalf("NewBatchID() returned inconsistent length: id[0]=%d id[%d]=%d", length, i, len(id))
			}
		}
	})

	t.Run("url_safe_chars", func(t *testing.T) {
		// Must be URL-safe: only hex or Crockford base32 characters.
		id := NewBatchID()
		for _, c := range id {
			// hex or uppercase Crockford base32 (0-9, A-Z minus I, L, O, U)
			if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z')) {
				// Also allow lowercase hex
				if !((c >= 'a' && c <= 'f')) {
					t.Fatalf("NewBatchID() returned non-URL-safe character %q in %q", c, id)
				}
			}
		}
		_ = strings.ToUpper(id) // sanity
	})

	t.Run("time_ordered", func(t *testing.T) {
		// Generate 100 IDs sequentially; each must be >= the previous.
		prev := NewBatchID()
		for i := 0; i < 100; i++ {
			next := NewBatchID()
			if next < prev {
				t.Fatalf("NewBatchID() ordering violated at iteration %d: %q < %q", i, next, prev)
			}
			prev = next
		}
	})
}
