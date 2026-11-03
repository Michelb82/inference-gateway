package capability

import (
	"fmt"
	"sync"
)

// Holder publishes immutable registry snapshots. Get and All return copies.
type Holder struct {
	mu  sync.RWMutex
	reg *Registry
}

func NewHolder() *Holder              { return &Holder{} }
func (h *Holder) Store(reg *Registry) { h.mu.Lock(); defer h.mu.Unlock(); h.reg = reg }

// Snapshot returns an immutable registry, safe to use after releasing the holder lock.
func (h *Holder) Snapshot() *Registry { h.mu.RLock(); defer h.mu.RUnlock(); return h.reg }
func (h *Holder) Get(name string) (Definition, error) {
	reg := h.Snapshot()
	if reg == nil {
		return Definition{}, fmt.Errorf("gateway is dormant; no manifest applied")
	}
	return reg.Get(name)
}
func (h *Holder) All() []Definition {
	reg := h.Snapshot()
	if reg == nil {
		return nil
	}
	return reg.All()
}
func (h *Holder) Ready() bool { return h.Snapshot() != nil }
