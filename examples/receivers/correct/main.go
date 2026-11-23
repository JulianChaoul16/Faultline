// Command correct is a webhook receiver that handles duplicates properly,
// for testing Faultline. Every Faultline scenario should PASS against it.
//
// The fix for the racy receiver is to make "have I seen this?" and "mark
// it as seen" one indivisible step (a claim). Only the copy that wins the
// claim does the work; every other copy, simultaneous or later, is
// acknowledged and skipped. In a real app the claim is usually a database
// insert into a table with a unique constraint on the event ID.
//
//	go run ./examples/receivers/correct
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

// ledger records how many times each event's side effect ran.
type ledger struct {
	mu     sync.Mutex
	counts map[string]int
}

// claim atomically checks and records id. It reports true for exactly one
// caller per id, no matter how many arrive at once.
func (l *ledger) claim(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts[id] > 0 {
		return false
	}
	l.counts[id]++
	return true
}

func (l *ledger) count(id string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.counts[id]
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	work := flag.Duration("work", 50*time.Millisecond, "simulated processing time")
	flag.Parse()

	log.Printf("correct receiver listening on %s", *addr)
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

		if !l.claim(e.ID) {
			// Still answer 2xx: the sender did nothing wrong, and an error
			// would only make it retry the duplicate again.
			log.Printf("duplicate %s ignored", e.ID)
			w.WriteHeader(http.StatusOK)
			return
		}

		// Only the winning copy gets here. (A production receiver would
		// also release the claim if this work fails, so a retry can redo it.)
		time.Sleep(work)

		log.Printf("processed %s (%s)", e.ID, e.Type)
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
