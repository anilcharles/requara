// Package events fans out live updates (mock-server calls) to browsers over
// Server-Sent Events.
package events

import (
	"encoding/json"
	"sync"
)

type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan []byte]struct{} // workspace id -> subscribers
}

func NewHub() *Hub { return &Hub{subs: map[string]map[chan []byte]struct{}{}} }

func (h *Hub) Subscribe(workspace string) (chan []byte, func()) {
	ch := make(chan []byte, 32)
	h.mu.Lock()
	if h.subs[workspace] == nil {
		h.subs[workspace] = map[chan []byte]struct{}{}
	}
	h.subs[workspace][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[workspace], ch)
		h.mu.Unlock()
	}
}

func (h *Hub) Publish(workspace string, e Event) {
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[workspace] {
		select {
		case ch <- b:
		default: // slow subscriber: drop rather than block
		}
	}
}
