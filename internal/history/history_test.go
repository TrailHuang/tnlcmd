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

// TestAddWrapAround 验证环形缓冲容量回绕：多次写满后逻辑顺序保持"最旧→最新"。
func TestAddWrapAround(t *testing.T) {
	h := NewCommandHistory(3)

	for _, cmd := range []string{"cmd1", "cmd2", "cmd3", "cmd4", "cmd5"} {
		h.Add(cmd)
	}

	if h.Len() != 3 {
		t.Fatalf("expected length 3, got %d", h.Len())
	}

	want := []string{"cmd3", "cmd4", "cmd5"}
	all := h.GetAll()
	for i, w := range want {
		if all[i] != w {
			t.Fatalf("GetAll() = %v, want %v", all, want)
		}
	}

	// 回绕后 Get 的逻辑下标同样保持正确
	for i, w := range want {
		if got := h.Get(i); got != w {
			t.Errorf("Get(%d) = %q, want %q", i, got, w)
		}
	}
	if got := h.Get(3); got != "" {
		t.Errorf("Get(3) = %q, want empty", got)
	}
}

// TestAddWrapAroundDedup 验证回绕后的连续去重语义仍按最新一条比较。
func TestAddWrapAroundDedup(t *testing.T) {
	h := NewCommandHistory(2)

	h.Add("cmd1")
	h.Add("cmd2")
	h.Add("cmd3") // 覆盖 cmd1，逻辑内容为 [cmd2, cmd3]
	h.Add("cmd3") // 与最新一条重复，应被去重

	if h.Len() != 2 {
		t.Fatalf("expected length 2, got %d", h.Len())
	}
	all := h.GetAll()
	if all[0] != "cmd2" || all[1] != "cmd3" {
		t.Errorf("GetAll() = %v, want [cmd2 cmd3]", all)
	}
}

// TestAddMaxSizeZero 验证容量为 0 时不保留任何历史且不 panic。
func TestAddMaxSizeZero(t *testing.T) {
	h := NewCommandHistory(0)

	h.Add("cmd1")
	if h.Len() != 0 {
		t.Errorf("expected length 0, got %d", h.Len())
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
	}

	<-done
}

// BenchmarkAddOverflow 测量容量已满时的 Add 开销。
// 环形实现下写入为 O(1)，各次迭代耗时应保持平稳（无 O(n) 拷贝导致的增长）。
func BenchmarkAddOverflow(b *testing.B) {
	h := NewCommandHistory(100)
	// 预填满缓冲区
	for i := 0; i < 100; i++ {
		h.Add("seed")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Add("cmd")
	}
}
