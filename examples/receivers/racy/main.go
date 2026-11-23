// Command racy is a webhook receiver with a subtle deduplication bug, for
// testing Faultline.
//
// It does check whether an event was already processed, but the check and
// the recording are two separate steps with work in between:
//
//  1. seen before?  -> no
//  2. do the work   (charge the card, send the email...)
//  3. mark as seen
//
// A duplicate that arrives later is caught at step 1. Duplicates that
// arrive at the same moment all pass step 1 before any of them reaches
// step 3, so every one of them does the work. Faultline's sequential
// scenario should PASS against it and the concurrent scenario should FAIL.
//
//	go run ./examples/receivers/racy
//
// POST /webhook      process an event: {"id": "...", "type": "..."}
// GET  /count?id=X   how many times event X was processed (for --check)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

type event struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// ledger records how many times each event's side effect ran. The mutex
// makes each method safe on its own; the bug is in how the handler
// combines them, not in the ledger.
type ledger struct {
	mu     sync.Mutex
	counts map[string]int
}

func (l *ledger) seen(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.counts[id] > 0
}

func (l *ledger) record(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.counts[id]++
}

func (l *ledger) count(id string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.counts[id]
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	work := flag.Duration("work", 50*time.Millisecond, "simulated processing time; the race window")
	flag.Parse()

	log.Printf("racy receiver listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, newHandler(*work)))
}

// newHandler builds the receiver's routes, separate from main so tests can
// drive it without opening a real port.
func newHandler(work time.Duration) http.Handler {
	l := &ledger{counts: map[string]int{}}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /webhook", func(w http.ResponseWriter, r *http.Request) {
		var e event
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil || e.ID == "" {
			http.Error(w, "invalid event", http.StatusBadRequest)
			return
		}

		if l.seen(e.ID) {
			log.Printf("duplicate %s ignored", e.ID)
			w.WriteHeader(http.StatusOK)
			return
		}

		// THE BUG: between the check above and the record below, other
		// copies of this event can also pass the check.
		time.Sleep(work)
		l.record(e.ID)

		log.Printf("processed %s (%s), total %d", e.ID, e.Type, l.count(e.ID))
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("GET /count", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		fmt.Fprintln(w, l.count(id))
	})

	return mux
}
