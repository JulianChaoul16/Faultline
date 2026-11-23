package oracle

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// pick returns the Windows (cmd.exe) or Unix (sh) form of a command.
func pick(windows, unix string) string {
	if runtime.GOOS == "windows" {
		return windows
	}
	return unix
}

func count(t *testing.T, template, id string) (Observation, error) {
	t.Helper()
	return Command{Template: template, Timeout: 10 * time.Second}.Count(context.Background(), id)
}

func TestCountParsesInteger(t *testing.T) {
	obs, err := count(t, "echo 1", "evt_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.Count != 1 {
		t.Fatalf("Count = %d, want 1", obs.Count)
	}
}

func TestTemplateSubstitutesID(t *testing.T) {
	obs, err := count(t, "echo {{id}}", "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.Count != 42 {
		t.Fatalf("Count = %d, want 42", obs.Count)
	}
}

func TestIDIsInEnvironment(t *testing.T) {
	obs, err := count(t, pick("echo %FAULTLINE_ID%", "echo $FAULTLINE_ID"), "7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.Count != 7 {
		t.Fatalf("Count = %d, want 7", obs.Count)
	}
}

// Pipes and the user's own quotes must reach the shell unchanged.
func TestShellFeaturesWork(t *testing.T) {
	template := pick(
		`if "a"=="a" echo 5 | findstr 5`,
		`[ "a" = "a" ] && echo 5 | grep 5`,
	)
	obs, err := count(t, template, "evt_1")
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr %q)", err, obs.Stderr)
	}
	if obs.Count != 5 {
		t.Fatalf("Count = %d, want 5", obs.Count)
	}
}

func TestNonIntegerOutputIsError(t *testing.T) {
	obs, err := count(t, "echo abc", "evt_1")
	if err == nil {
		t.Fatal("expected error for non-integer output")
	}
	if !strings.Contains(obs.Stdout, "abc") {
		t.Fatalf("Stdout = %q, want the raw output kept", obs.Stdout)
	}
}

func TestEmptyOutputIsError(t *testing.T) {
	if _, err := count(t, "exit 0", "evt_1"); err == nil {
		t.Fatal("expected error for empty output")
	}
}

func TestNegativeCountIsError(t *testing.T) {
	if _, err := count(t, "echo -1", "evt_1"); err == nil {
		t.Fatal("expected error for negative count")
	}
}

// A failing command must be an error even if it printed a number.
func TestNonZeroExitIsError(t *testing.T) {
	if _, err := count(t, "echo 1 && exit 3", "evt_1"); err == nil {
		t.Fatal("expected error for non-zero exit")
	}
}

func TestUnsafeIDIsRejectedBeforeRunning(t *testing.T) {
	for _, id := range []string{"", "a;b", "a b", `a"b`, "$(x)", "a&b", "a|b"} {
		if _, err := count(t, "echo 1", id); err == nil {
			t.Errorf("id %q: expected error", id)
		}
	}
}

func TestEmptyTemplateIsError(t *testing.T) {
	if _, err := count(t, "   ", "evt_1"); err == nil {
		t.Fatal("expected error for empty template")
	}
}

func TestTimeout(t *testing.T) {
	c := Command{
		Template: pick("ping -n 30 127.0.0.1 >nul", "sleep 30"),
		Timeout:  200 * time.Millisecond,
	}
	start := time.Now()
	_, err := c.Count(context.Background(), "evt_1")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Count took %s; timeout was not enforced", elapsed)
	}
}
