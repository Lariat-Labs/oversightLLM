// Package stream fans server events out to Server-Sent Events subscribers.
package stream

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type Event struct {
	Type           string `json:"type"`
	Agent          string `json:"agent,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`
	Data           any    `json:"data"`
}

type Filter struct {
	Agent          string
	ConversationID string
}

func (f Filter) match(e Event) bool {
	if f.Agent != "" && e.Agent != "" && e.Agent != f.Agent {
		return false
	}
	if f.ConversationID != "" && e.ConversationID != f.ConversationID && e.ConversationID != "" {
		return false
	}
	return true
}

type sub struct {
	ch     chan Event
	filter Filter
}

type Hub struct {
	mu   sync.Mutex
	subs map[*sub]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[*sub]struct{}{}} }

// Publish delivers e to every matching subscriber; slow subscribers drop events.
func (h *Hub) Publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if !s.filter.match(e) {
			continue
		}
		select {
		case s.ch <- e:
		default:
		}
	}
}

func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// Serve streams events to one client until it disconnects. initial events are sent first.
func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, f Filter, initial ...Event) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	s := &sub{ch: make(chan Event, 64), filter: f}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.subs, s)
		h.mu.Unlock()
	}()

	fmt.Fprint(w, "retry: 3000\n\n")
	for _, e := range initial {
		write(w, e)
	}
	fl.Flush()
	hb := time.NewTicker(20 * time.Second)
	defer hb.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-s.ch:
			write(w, e)
			fl.Flush()
		case t := <-hb.C:
			write(w, Event{Type: "heartbeat", Data: t.UTC()})
			fl.Flush()
		}
	}
}

func write(w http.ResponseWriter, e Event) {
	b, _ := json.Marshal(e)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, b)
}
