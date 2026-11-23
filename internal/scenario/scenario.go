// Package scenario delivers one event in a chaotic way and judges the
// receiver by asking the oracle what actually happened.
//
// One scenario run is:
//
//  1. pick a fresh event ID
//  2. deliver that same event Copies times (one after another, or all at once)
//  3. ask the oracle how many times the event was processed
//  4. compare with the expected count and produce a Verdict
package scenario

import (
	"context"
	"fmt"
	"sync"

	"faultline/internal/delivery"
	"faultline/internal/oracle"
)

// Scenario is one way of delivering duplicates.
type Scenario struct {
	Name       string
	Copies     int  // how many times the same event is delivered
	Concurrent bool // false: one after another; true: all at the same moment
}

// Defaults are the v1 duplicate scenarios.
//
// Sequential catches receivers with no deduplication at all. Concurrent
// catches receivers that check "have I seen this?" and then record it in
// two separate steps, which lets simultaneous copies all slip through.
var Defaults = []Scenario{
	{Name: "sequential-duplicates", Copies: 2},
	{Name: "concurrent-duplicates", Copies: 10, Concurrent: true},
}

// Counter reports how many times an event was processed. oracle.Command
// implements it; tests use an in-memory fake.
type Counter interface {
	Count(ctx context.Context, id string) (oracle.Observation, error)
}

// Config is everything a scenario run needs.
type Config struct {
	Target Target
	Client *delivery.Client
	Oracle Counter
	Expect int // expected processed count; the CLI defaults this to 1

	// NewID makes the event ID; nil means NewEventID. Tests override it.
	NewID func() string
}

// Verdict is the judgment on one scenario run.
type Verdict string

const (
	Pass Verdict = "PASS" // oracle count == Expect
	Fail Verdict = "FAIL" // oracle count != Expect: a receiver bug
	// Inconclusive means Faultline could not judge: the check broke, or the
	// receiver could not be reached at all. Never reported as a bug.
	Inconclusive Verdict = "INCONCLUSIVE"
)

// Outcome is everything observed in one scenario run, for the report.
type Outcome struct {
	Scenario    Scenario
	EventID     string
	Deliveries  []delivery.Result // in send order (start order for concurrent)
	Observation oracle.Observation
	Expect      int
	Verdict     Verdict
	Reason      string // one-line explanation of the verdict
}

// Run executes one scenario. Its error is only for misconfiguration
// (bad event body, Copies < 1); everything about the receiver is in Outcome.
func Run(ctx context.Context, cfg Config, s Scenario) (Outcome, error) {
	if s.Copies < 1 {
		return Outcome{}, fmt.Errorf("scenario %s: copies must be at least 1", s.Name)
	}
	newID := cfg.NewID
	if newID == nil {
		newID = NewEventID
	}
	id := newID()
	req, err := cfg.Target.Request(id)
	if err != nil {
		return Outcome{}, err
	}

	out := Outcome{Scenario: s, EventID: id, Expect: cfg.Expect}
	if s.Concurrent {
		out.Deliveries = sendConcurrent(ctx, cfg.Client, req, s.Copies)
	} else {
		out.Deliveries = sendSequential(ctx, cfg.Client, req, s.Copies)
	}

	if unreachable(out.Deliveries) {
		out.Verdict = Inconclusive
		out.Reason = fmt.Sprintf("receiver unreachable: %v", out.Deliveries[0].Err)
		return out, nil
	}

	obs, err := cfg.Oracle.Count(ctx, id)
	out.Observation = obs
	switch {
	case err != nil:
		out.Verdict = Inconclusive
		out.Reason = err.Error()
	case obs.Count == cfg.Expect:
		out.Verdict = Pass
		out.Reason = fmt.Sprintf("processed %d time(s), expected %d", obs.Count, cfg.Expect)
	default:
		out.Verdict = Fail
		out.Reason = fmt.Sprintf("processed %d time(s), expected %d", obs.Count, cfg.Expect)
	}
	return out, nil
}

func sendSequential(ctx context.Context, c *delivery.Client, req delivery.Request, n int) []delivery.Result {
	results := make([]delivery.Result, n)
	for i := range results {
		results[i] = c.Send(ctx, req)
	}
	return results
}

// sendConcurrent starts n goroutines that all wait on a start signal, then
// releases them together so the copies hit the receiver as close to
// simultaneously as possible.
func sendConcurrent(ctx context.Context, c *delivery.Client, req delivery.Request, n int) []delivery.Result {
	results := make([]delivery.Result, n)
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	ready.Add(n)
	done.Add(n)
	for i := range results {
		go func(i int) {
			defer done.Done()
			ready.Done()
			<-start
			results[i] = c.Send(ctx, req) // each goroutine writes only its own slot
		}(i)
	}
	ready.Wait()
	close(start)
	done.Wait()
	return results
}

// unreachable reports whether no delivery got any HTTP response at all.
func unreachable(results []delivery.Result) bool {
	for _, r := range results {
		if r.StatusCode != 0 {
			return false
		}
	}
	return true
}
