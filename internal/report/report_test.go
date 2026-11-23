package report

import (
	"errors"
	"strings"
	"testing"
	"time"

	"faultline/internal/delivery"
	"faultline/internal/oracle"
	"faultline/internal/scenario"
)

func ok(status int, d time.Duration) delivery.Result {
	return delivery.Result{StatusCode: status, Duration: d}
}

var (
	passed = scenario.Outcome{
		Scenario:   scenario.Scenario{Name: "sequential-duplicates", Copies: 2},
		EventID:    "flt_pass",
		Deliveries: []delivery.Result{ok(200, 5*time.Millisecond), ok(200, 7*time.Millisecond)},
		Verdict:    scenario.Pass,
		Reason:     "processed 1 time(s), expected 1",
	}
	failed = scenario.Outcome{
		Scenario: scenario.Scenario{Name: "concurrent-duplicates", Copies: 3, Concurrent: true},
		EventID:  "flt_fail",
		Deliveries: []delivery.Result{
			ok(200, 3*time.Millisecond), ok(409, 12*time.Millisecond), ok(200, 4*time.Millisecond),
		},
		Verdict: scenario.Fail,
		Reason:  "processed 2 time(s), expected 1",
	}
	inconclusive = scenario.Outcome{
		Scenario:    scenario.Scenario{Name: "sequential-duplicates", Copies: 2},
		EventID:     "flt_inc",
		Deliveries:  []delivery.Result{ok(200, time.Millisecond), ok(200, time.Millisecond)},
		Observation: oracle.Observation{Stdout: "{\"count\": 1}\n", Stderr: "warning: x\n"},
		Verdict:     scenario.Inconclusive,
		Reason:      `oracle: check command must print one integer, got "{\"count\": 1}"`,
	}
)

func render(t *testing.T, outs []scenario.Outcome, verbose bool) string {
	t.Helper()
	var b strings.Builder
	if err := Write(&b, outs, verbose); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestExitCodes(t *testing.T) {
	cases := []struct {
		outs []scenario.Outcome
		want int
	}{
		{nil, ExitPass},
		{[]scenario.Outcome{passed, passed}, ExitPass},
		{[]scenario.Outcome{passed, failed}, ExitFail},
		{[]scenario.Outcome{passed, inconclusive}, ExitInconclusive},
		{[]scenario.Outcome{inconclusive, failed}, ExitFail}, // a found bug wins
	}
	for i, c := range cases {
		if got := Summarize(c.outs).ExitCode(); got != c.want {
			t.Errorf("case %d: exit %d, want %d", i, got, c.want)
		}
	}
}

func TestPassIsOneLine(t *testing.T) {
	out := render(t, []scenario.Outcome{passed}, false)
	if strings.Contains(out, "flt_pass") || strings.Contains(out, "receiver answered") {
		t.Fatalf("PASS should be one line without details:\n%s", out)
	}
	if !strings.Contains(out, "PASS") || !strings.Contains(out, "1 passed, 0 failed, 0 inconclusive") {
		t.Fatalf("missing verdict or summary:\n%s", out)
	}
}

func TestFailShowsDeliveries(t *testing.T) {
	out := render(t, []scenario.Outcome{failed}, false)
	for _, want := range []string{
		"FAIL", "processed 2 time(s), expected 1",
		"event flt_fail, 3 copies, concurrent",
		"2 × 200 OK, 1 × 409 Conflict (3ms–12ms)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestInconclusiveShowsCheckOutput(t *testing.T) {
	out := render(t, []scenario.Outcome{inconclusive}, false)
	for _, want := range []string{"INCONCLUSIVE", "check stdout:", `{"count": 1}`, "check stderr:", "warning: x"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestVerboseShowsPassDetails(t *testing.T) {
	out := render(t, []scenario.Outcome{passed}, true)
	if !strings.Contains(out, "event flt_pass") || !strings.Contains(out, "2 × 200 OK") {
		t.Fatalf("verbose PASS missing details:\n%s", out)
	}
}

func TestTransportErrorsAreGrouped(t *testing.T) {
	refused := delivery.Result{Err: errors.New("connection refused")}
	got := deliverySummary([]delivery.Result{refused, refused, ok(200, 0)})
	if !strings.HasPrefix(got, "2 × error: connection refused, 1 × 200 OK") {
		t.Fatalf("got %q", got)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWriteErrorIsReturned(t *testing.T) {
	if err := Write(failingWriter{}, []scenario.Outcome{passed}, false); err == nil {
		t.Fatal("expected write error")
	}
}
