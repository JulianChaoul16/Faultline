package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func do(t *testing.T, h http.Handler, method, target, body string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

// The naive receiver must process duplicates every time — that is the bug
// Faultline exists to catch.
func TestDuplicatesAreProcessedEveryTime(t *testing.T) {
	h := newHandler()
	for i := 0; i < 3; i++ {
		if code, _ := do(t, h, "POST", "/webhook", `{"id":"evt_1","type":"payment"}`); code != 200 {
			t.Fatalf("delivery %d: status %d", i+1, code)
		}
	}
	if _, got := do(t, h, "GET", "/count?id=evt_1", ""); got != "3" {
		t.Fatalf("count evt_1 = %q, want 3", got)
	}
	if _, got := do(t, h, "GET", "/count?id=evt_2", ""); got != "0" {
		t.Fatalf("count evt_2 = %q, want 0", got)
	}
}

func TestRejectsBadInput(t *testing.T) {
	h := newHandler()
	cases := []struct {
		method, target, body string
		want                 int
	}{
		{"POST", "/webhook", `not json`, 400},
		{"POST", "/webhook", `{"type":"payment"}`, 400}, // missing id
		{"GET", "/webhook", "", 405},
		{"GET", "/count", "", 400},
	}
	for _, c := range cases {
		if code, _ := do(t, h, c.method, c.target, c.body); code != c.want {
			t.Errorf("%s %s %q: status %d, want %d", c.method, c.target, c.body, code, c.want)
		}
	}
}
