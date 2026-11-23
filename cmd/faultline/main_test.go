package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"faultline/internal/report"
)

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut strings.Builder
	code = run(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestUsageErrors(t *testing.T) {
	notObject := filepath.Join(t.TempDir(), "list.json")
	if err := os.WriteFile(notObject, []byte(`[1]`), 0o644); err != nil {
		t.Fatal(err)
	}
	const u, c = "--url=http://127.0.0.1:1/webhook", "--check=echo 1"

	cases := map[string][]string{
		"no args":         {},
		"no url":          {c},
		"no check":        {u},
		"bad url":         {"--url=localhost:8080", c},
		"ftp url":         {"--url=ftp://x/y", c},
		"unknown flag":    {u, c, "--nope"},
		"stray argument":  {u, c, "extra"},
		"bad scenario":    {u, c, "--scenario=sideways"},
		"negative expect": {u, c, "--expect=-1"},
		"missing body":    {u, c, "--body=does-not-exist.json"},
		"body not object": {u, c, "--body=" + notObject},
		"bad header":      {u, c, "--header=NoColon"},
		"no id anywhere":  {u, c, "--id-field="},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := runCLI(t, args...)
			if code != report.ExitUsage {
				t.Fatalf("exit %d, want %d\nstderr: %s", code, report.ExitUsage, stderr)
			}
			if stdout != "" {
				t.Fatalf("nothing should run on a usage error, got stdout:\n%s", stdout)
			}
		})
	}
}

func TestHelpExitsZero(t *testing.T) {
	code, _, stderr := runCLI(t, "-h")
	if code != 0 || !strings.Contains(stderr, "Usage: faultline") {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
}

func TestPowerShellEscapedURL(t *testing.T) {
	got, err := parseURL(`http\://localhost:8080/webhook`)
	if err != nil || got != "http://localhost:8080/webhook" {
		t.Fatalf("got %q, %v", got, err)
	}
}

// receiver is a test webhook receiver with a /count endpoint, like the
// examples. dedup=false behaves like the naive receiver.
func receiver(t *testing.T, dedup bool) *httptest.Server {
	var mu sync.Mutex
	counts := map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook", func(w http.ResponseWriter, r *http.Request) {
		var e struct{ ID string }
		if json.NewDecoder(r.Body).Decode(&e) != nil || e.ID == "" {
			http.Error(w, "bad event", http.StatusBadRequest)
			return
		}
		mu.Lock()
		if !dedup || counts[e.ID] == 0 {
			counts[e.ID]++
		}
		mu.Unlock()
	})
	mux.HandleFunc("GET /count", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintln(w, counts[r.URL.Query().Get("id")])
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// endToEnd runs the real CLI, with a real curl check command, against srv.
func endToEnd(t *testing.T, srv *httptest.Server, extra ...string) (int, string) {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	args := append([]string{
		"--url", srv.URL + "/webhook",
		"--check", `curl -s "` + srv.URL + `/count?id={{id}}"`,
	}, extra...)
	code, stdout, stderr := runCLI(t, args...)
	if stderr != "" {
		t.Logf("stderr: %s", stderr)
	}
	return code, stdout
}

func TestEndToEndNaiveReceiverFails(t *testing.T) {
	code, out := endToEnd(t, receiver(t, false))
	if code != report.ExitFail {
		t.Fatalf("exit %d, want %d\n%s", code, report.ExitFail, out)
	}
	if !strings.Contains(out, "0 passed, 2 failed, 0 inconclusive") {
		t.Fatalf("unexpected report:\n%s", out)
	}
}

func TestEndToEndCorrectReceiverPasses(t *testing.T) {
	code, out := endToEnd(t, receiver(t, true))
	if code != report.ExitPass {
		t.Fatalf("exit %d, want %d\n%s", code, report.ExitPass, out)
	}
}

func TestEndToEndScenarioAndExpectFlags(t *testing.T) {
	// The naive receiver processes 2 sequential copies twice; with
	// --expect 2 that counts as correct.
	code, out := endToEnd(t, receiver(t, false), "--scenario", "sequential-duplicates", "--expect", "2")
	if code != report.ExitPass || !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestEndToEndBrokenCheckIsInconclusive(t *testing.T) {
	code, out := endToEnd(t, receiver(t, true), "--check", "echo not-a-number")
	if code != report.ExitInconclusive {
		t.Fatalf("exit %d, want %d\n%s", code, report.ExitInconclusive, out)
	}
}
