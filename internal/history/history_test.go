package history

import (
	"testing"
)

func TestNewCommandHistory(t *testing.T) {
	h := NewCommandHistory(10)
	if h == nil {
		t.Fatal("NewCommandHistory returned nil")
	}
	if h.Len() != 0 {
		t.Errorf("expected length 0, got %d", h.Len())
	}
	if h.maxSize != 10 {
		t.Errorf("expected maxSize 10, got %d", h.maxSize)
	}
}

func TestAdd(t *testing.T) {
	h := NewCommandHistory(5)

	h.Add("cmd1")
	if h.Len() != 1 {
		t.Errorf("expected length 1, got %d", h.Len())
	}

	h.Add("cmd2")
	if h.Len() != 2 {
		t.Errorf("expected length 2, got %d", h.Len())
	}
}

func TestAddDedupConsecutive(t *testing.T) {
	h := NewCommandHistory(5)

	h.Add("cmd1")
	h.Add("cmd1") // consecutive duplicate, should be deduped
	if h.Len() != 1 {
		t.Errorf("expected length 1 after dedup, got %d", h.Len())
	}

	h.Add("cmd2")
	h.Add("cmd1") // not consecutive, should be added
	if h.Len() != 3 {
		t.Errorf("expected length 3, got %d", h.Len())
	}
}

func TestAddOverflow(t *testing.T) {
	h := NewCommandHistory(3)

	h.Add("cmd1")
	h.Add("cmd2")
	h.Add("cmd3")
	h.Add("cmd4") // should evict cmd1

	if h.Len() != 3 {
		t.Errorf("expected length 3, got %d", h.Len())
	}

	all := h.GetAll()
	if all[0] != "cmd2" {
		t.Errorf("expected first element to be cmd2, got %s", all[0])
	}
	if all[2] != "cmd4" {
		t.Errorf("expected last element to be cmd4, got %s", all[2])
	}
}

func TestGet(t *testing.T) {
	h := NewCommandHistory(10)

	h.Add("cmd1")
	h.Add("cmd2")
	h.Add("cmd3")

	tests := []struct {
		index    int
		expected string
	}{
		{0, "cmd1"},
		{1, "cmd2"},
		{2, "cmd3"},
		{-1, ""},
		{3, ""},
	}

	for _, tt := range tests {
		got := h.Get(tt.index)
		if got != tt.expected {
			t.Errorf("Get(%d) = %q, want %q", tt.index, got, tt.expected)
		}
	}
}

func TestGetAll(t *testing.T) {
	h := NewCommandHistory(10)

	h.Add("cmd1")
	h.Add("cmd2")

	all := h.GetAll()
	if len(all) != 2 {
		t.Errorf("expected 2 elements, got %d", len(all))
	}
	if all[0] != "cmd1" || all[1] != "cmd2" {
		t.Errorf("unexpected GetAll result: %v", all)
	}

	// Verify it's a copy, not the internal slice
	all[0] = "modified"
	if h.Get(0) != "cmd1" {
		t.Errorf("GetAll should return a copy")
	}
}

func TestPreviousNext_navigation(t *testing.T) {
	h := NewCommandHistory(10)

	// Empty history
	if h.Previous() != "" {
		t.Errorf("Previous on empty history should return empty string")
	}
	if h.Next() != "" {
		t.Errorf("Next on empty history should return empty string")
	}

	h.Add("cmd1")
	h.Add("cmd2")
	h.Add("cmd3")

	// First Previous: position already at last after Add(), so moves to second-to-last
	got := h.Previous()
	if got != "cmd2" {
		t.Errorf("first Previous() = %q, want %q", got, "cmd2")
	}

	// Second Previous: cmd1
	got = h.Previous()
	if got != "cmd1" {
		t.Errorf("second Previous() = %q, want %q", got, "cmd1")
	}

	// Third Previous: stay at cmd1
	got = h.Previous()
	if got != "cmd1" {
		t.Errorf("third Previous() = %q, want %q (stay at first)", got, "cmd1")
	}

	// Next: back to cmd2
	got = h.Next()
	if got != "cmd2" {
		t.Errorf("first Next() = %q, want %q", got, "cmd2")
	}

	// Next: cmd3
	got = h.Next()
	if got != "cmd3" {
		t.Errorf("second Next() = %q, want %q", got, "cmd3")
	}

	// Next: past end, should return empty
	got = h.Next()
	if got != "" {
		t.Errorf("Next() past end = %q, want empty", got)
	}
}

func TestResetPosition(t *testing.T) {
	h := NewCommandHistory(10)

	h.Add("cmd1")
	h.Add("cmd2")

	// After Add, position is at last element (cmd2)
	// ResetPosition sets it to -1
	h.ResetPosition()

	// After reset, Previous should start from last again
	got := h.Previous()
	if got != "cmd2" {
		t.Errorf("after ResetPosition, Previous() = %q, want %q", got, "cmd2")
	}
}

func TestConcurrentAccess(t *testing.T) {
	h := NewCommandHistory(100)

	done := make(chan bool)
	go func() {
		for i := 0; i < 100; i++ {
			h.Add("concurrent-cmd")
		}
		done <- true
	}()

	for i := 0; i < 100; i++ {
		_ = h.Len()
		_ = h.GetAll()
		_ = h.Previous()
		_ = h.Next()
	}

	<-done
}
