package tool

import (
	"fmt"
	"strings"
	"sync"
)

type Definition struct {
	Name        string
	Endpoint    string
	Instruction string
}

type Holder struct {
	mu    sync.RWMutex
	tools map[string]Definition
}

func NewHolder() *Holder { return &Holder{tools: map[string]Definition{}} }

func (h *Holder) Store(tools map[string]Definition) {
	copied := make(map[string]Definition, len(tools))
	for name, definition := range tools {
		copied[name] = definition
	}
	h.mu.Lock()
	h.tools = copied
	h.mu.Unlock()
}

func (h *Holder) Get(name string) (Definition, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	definition, ok := h.tools[strings.TrimSpace(name)]
	if !ok {
		return Definition{}, fmt.Errorf("unknown tool: %s", name)
	}
	return definition, nil
}
