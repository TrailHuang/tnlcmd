package history

import (
	"sync"
)

type CommandHistory struct {
	mu       sync.RWMutex
	history  []string
	maxSize  int
	position int
}

func NewCommandHistory(maxSize int) *CommandHistory {
	return &CommandHistory{
		history:  make([]string, 0, maxSize),
		maxSize:  maxSize,
		position: -1,
	}
}

func (h *CommandHistory) Add(cmd string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.history) > 0 && h.history[len(h.history)-1] == cmd {
		return
	}

	if len(h.history) >= h.maxSize {
		h.history = h.history[1:]
	}

	h.history = append(h.history, cmd)
	h.position = len(h.history) - 1
}

func (h *CommandHistory) Get(index int) string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if index < 0 || index >= len(h.history) {
		return ""
	}

	return h.history[index]
}

func (h *CommandHistory) GetAll() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make([]string, len(h.history))
	copy(result, h.history)
	return result
}

func (h *CommandHistory) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.history)
}

func (h *CommandHistory) Previous() string {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.history) == 0 {
		return ""
	}

	if h.position < 0 {
		h.position = len(h.history) - 1
	} else if h.position > 0 {
		h.position--
	}

	return h.history[h.position]
}

func (h *CommandHistory) Next() string {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.history) == 0 || h.position < 0 {
		return ""
	}

	if h.position < len(h.history)-1 {
		h.position++
		return h.history[h.position]
	} else {
		h.position = -1
		return ""
	}
}

func (h *CommandHistory) ResetPosition() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.position = -1
}
