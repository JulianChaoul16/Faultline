// Command naive is a deliberately buggy webhook receiver for testing Faultline.
//
// It processes every delivery it receives, with no deduplication, so sending
// the same event twice performs the side effect twice. Faultline's duplicate
// scenarios should FAIL against it.
//
//	go run ./examples/receivers/naive
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
)

type event struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// ledger records how many times each event's side effect ran. In a real app
// this would be a database table (payments, emails sent, credits granted).
type ledger struct {
	mu     sync.Mutex
	counts map[string]int
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
	flag.Parse()

	log.Printf("naive receiver listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, newHandler()))
}

// newHandler builds the receiver's routes, separate from main so tests can
// drive it without opening a real port.
func newHandler() http.Handler {
	l := &ledger{counts: map[string]int{}}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /webhook", func(w http.ResponseWriter, r *http.Request) {
		var e event
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil || e.ID == "" {
			http.Error(w, "invalid event", http.StatusBadRequest)
			return
		}

		// THE BUG: no check for whether e.ID was already processed.
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
