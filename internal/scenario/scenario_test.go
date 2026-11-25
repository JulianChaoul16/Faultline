package scenario

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"faultline/internal/delivery"
	"faultline/internal/oracle"
)

// fakeReceiver is an in-memory webhook receiver with four behaviours,
// mirroring the naive / racy / correct example receivers plus "forgetful".
type fakeReceiver struct {
	mode string // "naive", "racy", "correct" or "forgetful"

	mu       sync.Mutex
	counts   map[string]int
	lastSeen map[string]time.Time // forgetful only
	inFlight int
	maxIn    int // most requests seen in the handler at the same time
}

// forgetfulTTL is how long the forgetful mode remembers an event. Back to
// back copies arrive well inside it; testDelay copies arrive after it.
const (
	forgetfulTTL = 200 * time.Millisecond
	testDelay    = 2 * forgetfulTTL
)

func newFakeReceiver(mode string) *fakeReceiver {
	return &fakeReceiver{mode: mode, counts: map[string]int{}, lastSeen: map[string]time.Time{}}
}

func (f *fakeReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var e struct{ ID string }
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil || e.ID == "" {
		http.Error(w, "bad event", http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.inFlight++
	f.maxIn = max(f.maxIn, f.inFlight)
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.inFlight--; f.mu.Unlock() }()

	switch f.mode {
	case "naive":
		f.mu.Lock()
		f.counts[e.ID]++
		f.mu.Unlock()
	case "racy":
		// Check, then (after some "work") record: two separate steps.
		f.mu.Lock()
		seen := f.counts[e.ID] > 0
		f.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		if !seen {
			f.mu.Lock()
			f.counts[e.ID]++
			f.mu.Unlock()
		}
	case "correct":
		// Check and record in one atomic step.
		f.mu.Lock()
		if f.counts[e.ID] == 0 {
			f.counts[e.ID]++
		}
		f.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	case "forgetful":
		// Atomic like "correct", but the memory of an event expires.
		f.mu.Lock()
		if seen, ok := f.lastSeen[e.ID]; !ok || time.Since(seen) > forgetfulTTL {
			f.counts[e.ID]++
			f.lastSeen[e.ID] = time.Now()
		}
		f.mu.Unlock()
	}
	w.WriteHeader(http.StatusOK)
}

// Count makes fakeReceiver its own oracle.
func (f *fakeReceiver) Count(_ context.Context, id string) (oracle.Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return oracle.Observation{Count: f.counts[id]}, nil
}

func run(t *testing.T, mode string, s Scenario) (Outcome, *fakeReceiver) {
	t.Helper()
	return runCtx(t, context.Background(), mode, s)
}

func runCtx(t *testing.T, ctx context.Context, mode string, s Scenario) (Outcome, *fakeReceiver) {
	t.Helper()
	recv := newFakeReceiver(mode)
	srv := httptest.NewServer(recv)
	t.Cleanup(srv.Close)

	cfg := Config{
		Target: Target{URL: srv.URL, Body: []byte(`{"type":"payment.succeeded"}`)},
		Client: delivery.NewClient(5 * time.Second),
		Oracle: recv,
		Expect: 1,
	}
	out, err := Run(ctx, cfg, s)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out, recv
}

var (
	sequential = Defaults[0]
	concurrent = Defaults[1]
	// delayed uses a short test delay; the real default is far too slow
	// for unit tests.
	delayed = Scenario{Name: "test-delayed", Copies: 2, Delay: testDelay}
)

// The verdict table v1 must get right.
func TestVerdicts(t *testing.T) {
	cases := []struct {
		mode string
		s    Scenario
		want Verdict
	}{
		{"naive", sequential, Fail},
		{"naive", concurrent, Fail},
		{"racy", sequential, Pass},
		{"racy", concurrent, Fail},
		{"correct", sequential, Pass},
		{"correct", concurrent, Pass},
		{"correct", delayed, Pass},
		// The bug only a delay reveals: forgetful dedups fine back to back.
		{"forgetful", sequential, Pass},
		{"forgetful", concurrent, Pass},
		{"forgetful", delayed, Fail},
	}
	for _, c := range cases {
		t.Run(c.mode+"/"+c.s.Name, func(t *testing.T) {
			out, _ := run(t, c.mode, c.s)
			if out.Verdict != c.want {
				t.Fatalf("verdict = %s (%s), want %s", out.Verdict, out.Reason, c.want)
			}
			if len(out.Deliveries) != c.s.Copies {
				t.Fatalf("%d deliveries, want %d", len(out.Deliveries), c.s.Copies)
			}
			for i, d := range out.Deliveries {
				if !d.OK() {
					t.Fatalf("delivery %d not OK: status %d err %v", i, d.StatusCode, d.Err)
				}
			}
		})
	}
}

func TestConcurrentCopiesOverlap(t *testing.T) {
	_, recv := run(t, "correct", concurrent)
	if recv.maxIn < 2 {
		t.Fatalf("max in-flight = %d; concurrent copies never overlapped", recv.maxIn)
	}
}

func TestSequentialCopiesDoNotOverlap(t *testing.T) {
	_, recv := run(t, "correct", sequential)
	if recv.maxIn != 1 {
		t.Fatalf("max in-flight = %d, want 1", recv.maxIn)
	}
}

func TestDelayIsBetweenCopies(t *testing.T) {
	out, _ := run(t, "correct", delayed)
	first, second := out.Deliveries[0], out.Deliveries[1]
	gap := second.Start.Sub(first.Start.Add(first.Duration))
	if gap < testDelay {
		t.Fatalf("gap between response and next copy = %s, want at least %s", gap, testDelay)
	}
}

// Ctrl+C during a long delay must stop promptly and must not PASS: the
// receiver only saw one copy, which proves nothing about deduplication.
func TestCancelDuringDelayIsInconclusive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	long := Scenario{Name: "long", Copies: 2, Delay: time.Minute}

	begin := time.Now()
	out, _ := runCtx(t, ctx, "correct", long)
	if took := time.Since(begin); took > 5*time.Second {
		t.Fatalf("Run took %s after cancel; the delay was not interruptible", took)
	}
	if out.Verdict != Inconclusive {
		t.Fatalf("verdict = %s (%s), want INCONCLUSIVE", out.Verdict, out.Reason)
	}
	if len(out.Deliveries) != 1 {
		t.Fatalf("%d deliveries, want 1 (only the copies actually sent)", len(out.Deliveries))
	}
}

func TestEachRunGetsFreshID(t *testing.T) {
	a, _ := run(t, "correct", sequential)
	b, _ := run(t, "correct", sequential)
	if a.EventID == b.EventID {
		t.Fatalf("both runs used ID %s", a.EventID)
	}
}

type brokenOracle struct{}

func (brokenOracle) Count(context.Context, string) (oracle.Observation, error) {
	return oracle.Observation{Stdout: "oops"}, errors.New("check command failed")
}

func TestBrokenOracleIsInconclusive(t *testing.T) {
	srv := httptest.NewServer(newFakeReceiver("naive"))
	defer srv.Close()
	cfg := Config{
		Target: Target{URL: srv.URL, Body: []byte(`{}`)},
		Client: delivery.NewClient(5 * time.Second),
		Oracle: brokenOracle{},
		Expect: 1,
	}
	out, err := Run(context.Background(), cfg, sequential)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Verdict != Inconclusive {
		t.Fatalf("verdict = %s, want INCONCLUSIVE", out.Verdict)
	}
	if out.Observation.Stdout != "oops" {
		t.Fatalf("observation not kept for the report: %+v", out.Observation)
	}
}

func TestUnreachableReceiverIsInconclusive(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listening any more

	cfg := Config{
		Target: Target{URL: url, Body: []byte(`{}`)},
		Client: delivery.NewClient(2 * time.Second),
		Oracle: brokenOracle{}, // must not even be asked
		Expect: 1,
	}
	out, err := Run(context.Background(), cfg, sequential)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Verdict != Inconclusive {
		t.Fatalf("verdict = %s, want INCONCLUSIVE", out.Verdict)
	}
}

func TestBadConfigIsError(t *testing.T) {
	cfg := Config{Target: Target{URL: "http://x", Body: []byte(`[1,2]`)}, Client: delivery.NewClient(time.Second)}
	if _, err := Run(context.Background(), cfg, sequential); err == nil {
		t.Fatal("expected error for non-object body")
	}
	if _, err := Run(context.Background(), cfg, Scenario{Name: "zero", Copies: 0}); err == nil {
		t.Fatal("expected error for zero copies")
	}
	if _, err := Run(context.Background(), cfg, Scenario{Name: "both", Copies: 2, Concurrent: true, Delay: time.Second}); err == nil {
		t.Fatal("expected error for delay on concurrent copies")
	}
	if _, err := Run(context.Background(), cfg, Scenario{Name: "neg", Copies: 2, Delay: -time.Second}); err == nil {
		t.Fatal("expected error for negative delay")
	}
}

func TestRequestWritesIDFieldAndHeader(t *testing.T) {
	tgt := Target{
		URL:      "http://x",
		Body:     []byte(`{"id":"old","type":"t","data":{"amount":5}}`),
		IDField:  "event_id",
		IDHeader: "Idempotency-Key",
		Header:   http.Header{"X-Test": {"1"}},
	}
	req, err := tgt.Request("flt_abc")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(req.Body, &got); err != nil {
		t.Fatal(err)
	}
	if got["event_id"] != "flt_abc" || got["id"] != "old" || got["type"] != "t" || got["data"] == nil {
		t.Fatalf("body = %s", req.Body)
	}
	if req.Header.Get("Idempotency-Key") != "flt_abc" || req.Header.Get("X-Test") != "1" {
		t.Fatalf("header = %v", req.Header)
	}
	if tgt.Header.Get("Idempotency-Key") != "" {
		t.Fatal("Request modified the Target's own header map")
	}
}

func TestRequestNoIDFieldLeavesBodyAlone(t *testing.T) {
	body := []byte(`not even json`)
	req, err := Target{Body: body, NoIDField: true, IDHeader: "X-Id"}.Request("flt_1")
	if err != nil {
		t.Fatal(err)
	}
	if string(req.Body) != string(body) || req.Header.Get("X-Id") != "flt_1" {
		t.Fatalf("req = %+v", req)
	}
}

func TestNewEventIDIsOracleSafe(t *testing.T) {
	// The oracle rejects unsafe IDs; make sure ours always pass.
	o := oracle.Command{Template: "echo 0", Timeout: 5 * time.Second}
	if _, err := o.Count(context.Background(), NewEventID()); err != nil {
		t.Fatalf("generated ID rejected by oracle: %v", err)
	}
}
