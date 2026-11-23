// Package report turns scenario outcomes into human-readable output and a
// process exit code.
//
// Output is quiet when things pass and detailed when they don't: a PASS is
// one line, a FAIL shows what the receiver answered, and an INCONCLUSIVE
// shows what the check command printed so the user can fix it.
package report

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"faultline/internal/delivery"
	"faultline/internal/scenario"
)

// Exit codes. 2 is reserved for command-line usage errors (set by the CLI).
const (
	ExitPass         = 0 // every scenario passed
	ExitFail         = 1 // at least one receiver bug found
	ExitUsage        = 2 // bad flags or config; nothing was run
	ExitInconclusive = 3 // no bugs found, but at least one scenario couldn't be judged
)

// Summary counts verdicts.
type Summary struct {
	Passed, Failed, Inconclusive int
}

// Summarize counts the verdicts in outs.
func Summarize(outs []scenario.Outcome) Summary {
	var s Summary
	for _, o := range outs {
		switch o.Verdict {
		case scenario.Pass:
			s.Passed++
		case scenario.Fail:
			s.Failed++
		default:
			s.Inconclusive++
		}
	}
	return s
}

// ExitCode picks the process exit code. A found bug outranks an
// inconclusive check: if anything failed, the receiver is known to be broken.
func (s Summary) ExitCode() int {
	switch {
	case s.Failed > 0:
		return ExitFail
	case s.Inconclusive > 0:
		return ExitInconclusive
	default:
		return ExitPass
	}
}

// Write prints a report for outs to w. With verbose, every scenario shows
// its delivery details, not just the ones that did not pass.
func Write(w io.Writer, outs []scenario.Outcome, verbose bool) error {
	// Build the whole report first so there is a single write to check.
	var b strings.Builder
	nameWidth := 0
	for _, o := range outs {
		nameWidth = max(nameWidth, len(o.Scenario.Name))
	}

	for _, o := range outs {
		fmt.Fprintf(&b, "%-12s %-*s  %s\n", o.Verdict, nameWidth, o.Scenario.Name, o.Reason)
		if o.Verdict == scenario.Pass && !verbose {
			continue
		}
		mode := "sequential"
		if o.Scenario.Concurrent {
			mode = "concurrent"
		}
		fmt.Fprintf(&b, "%12s event %s, %d copies, %s\n", "", o.EventID, o.Scenario.Copies, mode)
		fmt.Fprintf(&b, "%12s receiver answered: %s\n", "", deliverySummary(o.Deliveries))
		if o.Verdict == scenario.Inconclusive || verbose {
			writeOutput(&b, "check stdout", o.Observation.Stdout)
			writeOutput(&b, "check stderr", o.Observation.Stderr)
		}
	}

	s := Summarize(outs)
	fmt.Fprintf(&b, "\n%d passed, %d failed, %d inconclusive\n", s.Passed, s.Failed, s.Inconclusive)

	_, err := io.WriteString(w, b.String())
	return err
}

// deliverySummary groups results by what the receiver answered, e.g.
// "9 × 200 OK, 1 × 409 Conflict (4ms–31ms)".
func deliverySummary(results []delivery.Result) string {
	if len(results) == 0 {
		return "nothing sent"
	}
	var order []string
	counts := map[string]int{}
	lo, hi := results[0].Duration, results[0].Duration
	for _, r := range results {
		key := answer(r)
		if counts[key] == 0 {
			order = append(order, key)
		}
		counts[key]++
		lo, hi = min(lo, r.Duration), max(hi, r.Duration)
	}

	parts := make([]string, len(order))
	for i, key := range order {
		parts[i] = fmt.Sprintf("%d × %s", counts[key], key)
	}
	return fmt.Sprintf("%s (%s–%s)", strings.Join(parts, ", "), ms(lo), ms(hi))
}

func answer(r delivery.Result) string {
	if r.Err != nil && r.StatusCode == 0 {
		return "error: " + r.Err.Error()
	}
	return strings.TrimSpace(fmt.Sprintf("%d %s", r.StatusCode, http.StatusText(r.StatusCode)))
}

func ms(d time.Duration) string {
	if d < time.Millisecond {
		return "<1ms" // local receivers are often faster than 1ms; "0s" reads as broken
	}
	return d.Round(time.Millisecond).String()
}

func writeOutput(b *strings.Builder, label, text string) {
	text = strings.TrimRight(text, "\r\n")
	if text == "" {
		return
	}
	fmt.Fprintf(b, "%12s %s:\n", "", label)
	for _, line := range strings.Split(text, "\n") {
		fmt.Fprintf(b, "%14s %s\n", "", strings.TrimRight(line, "\r"))
	}
}
