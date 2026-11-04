package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/kentrow/tapyr/internal/clock"
)

// Event types of the event stream.
const (
	EventRunFinished    = "run_finished"
	EventPlanChanged    = "plan_changed"
	EventCircuitChanged = "circuit_changed"
	EventConfigReloaded = "config_reloaded"
)

// heartbeat is the interval of the comment lines that keep proxies from
// closing an idle stream.
const heartbeat = 15 * time.Second

// subscriberBuffer is the number of events a slow client may lag behind
// before it loses events.
const subscriberBuffer = 64

// Event is an event of the stream: its type and its fields, sent as a JSON
// object with the time of the event.
type Event struct {
	Type   string
	Time   time.Time
	Fields map[string]any
}

// Broker fans the events of the collector out to the clients of the event
// stream. Publishing never blocks: a client that does not keep up loses
// events.
type Broker struct {
	clock clock.Clock

	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// NewBroker returns a Broker.
func NewBroker(clk clock.Clock) *Broker {
	if clk == nil {
		clk = clock.Real()
	}
	return &Broker{clock: clk, subs: map[chan Event]struct{}{}}
}

// Publish sends an event to every client.
func (b *Broker) Publish(typ string, fields map[string]any) {
	if b == nil {
		return
	}
	e := Event{Type: typ, Time: b.clock.Now(), Fields: fields}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (b *Broker) subscribe() (chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

// serve streams the events as Server-Sent Events until the client goes.
func (b *Broker) serve(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal", "streaming is not supported")
		return
	}
	ch, unsubscribe := b.subscribe()
	defer unsubscribe()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	tick := b.clock.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C():
			fmt.Fprint(w, ": heartbeat\n\n")
		case e := <-ch:
			data := make(map[string]any, len(e.Fields)+1)
			for k, v := range e.Fields {
				data[k] = v
			}
			data["time"] = e.Time.UTC()
			raw, err := json.Marshal(data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, raw)
		}
		flusher.Flush()
	}
}
