package history

import (
	"sync"
)

// CommandHistory 命令历史环形缓冲区。
// 底层使用固定容量的环形数组，容量超限时覆盖最旧记录，Add 为 O(1)。
type CommandHistory struct {
	mu      sync.RWMutex
	buf     []string // 固定容量的环形存储
	start   int      // 最旧记录在 buf 中的物理下标
	size    int      // 当前有效记录数
	maxSize int      // 容量上限
}

// NewCommandHistory 创建命令历史。maxSize <= 0 表示不保留历史。
func NewCommandHistory(maxSize int) *CommandHistory {
	h := &CommandHistory{
		maxSize: maxSize,
	}
	if maxSize > 0 {
		h.buf = make([]string, maxSize)
	}
	return h
}

// at 返回逻辑序第 i 条记录，调用方必须持有锁。
func (h *CommandHistory) at(i int) string {
	return h.buf[(h.start+i)%h.maxSize]
}

// Add 追加一条命令。容量满时覆盖最旧记录，整体为 O(1)。
func (h *CommandHistory) Add(cmd string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.maxSize <= 0 {
		h.buf = nil
		h.start = 0
		h.size = 0
		return
	}

	// 连续重复的命令不入库
	if h.size > 0 && h.at(h.size-1) == cmd {
		return
	}

	if h.size < h.maxSize {
		// 未满：写入尾部
		h.buf[(h.start+h.size)%h.maxSize] = cmd
		h.size++
	} else {
		// 已满：覆盖最旧记录并前移起点
		h.buf[h.start] = cmd
		h.start = (h.start + 1) % h.maxSize
	}
}

// Get 按逻辑下标获取记录，越界返回空字符串。
func (h *CommandHistory) Get(index int) string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if index < 0 || index >= h.size {
		return ""
	}
	return h.at(index)
}

// GetAll 按逻辑顺序返回全部记录的副本。
func (h *CommandHistory) GetAll() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make([]string, h.size)
	for i := range result {
		result[i] = h.at(i)
	}
	return result
}

// Len 返回当前记录数。
func (h *CommandHistory) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.size
}
