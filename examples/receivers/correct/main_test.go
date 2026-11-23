package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func do(t *testing.T, h http.Handler, method, target, body string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

func TestSequentialDuplicatesAreIgnored(t *testing.T) {
	h := newHandler(0)
	for i := 0; i < 3; i++ {
		if code, _ := do(t, h, "POST", "/webhook", `{"id":"evt_1"}`); code != 200 {
			t.Fatalf("delivery %d: status %d", i+1, code)
		}
	}
	if _, got := do(t, h, "GET", "/count?id=evt_1", ""); got != "1" {
		t.Fatalf("count = %q, want 1", got)
	}
}

// The same setup that breaks the racy receiver must not break this one.
func TestConcurrentDuplicatesAreIgnored(t *testing.T) {
	h := newHandler(50 * time.Millisecond)
	var wg sync.WaitGroup
	codes := make([]int, 5)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("POST", "/webhook", strings.NewReader(`{"id":"evt_1"}`)))
			codes[i] = rec.Code
		}(i)
	}
	wg.Wait()
	for i, c := range codes {
		if c != 200 {
			t.Errorf("copy %d: status %d, want 200 (duplicates are still acknowledged)", i, c)
		}
	}
	if _, got := do(t, h, "GET", "/count?id=evt_1", ""); got != "1" {
		t.Fatalf("count = %q, want 1", got)
	}
}

func TestRejectsBadInput(t *testing.T) {
	h := newHandler(0)
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
