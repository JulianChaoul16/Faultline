package delivery

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSendRecordsSuccess(t *testing.T) {
	var gotBody, gotType, gotCustom string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotType = r.Header.Get("Content-Type")
		gotCustom = r.Header.Get("Webhook-Id")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	res := NewClient(time.Second).Send(context.Background(), Request{
		URL:    srv.URL,
		Body:   []byte(`{"id":"evt_1"}`),
		Header: http.Header{"Webhook-Id": {"msg_1"}},
	})

	if !res.OK() || res.StatusCode != 200 || string(res.Body) != "ok" {
		t.Fatalf("got %+v", res)
	}
	if gotBody != `{"id":"evt_1"}` || gotType != "application/json" || gotCustom != "msg_1" {
		t.Fatalf("server saw body=%q type=%q id=%q", gotBody, gotType, gotCustom)
	}
}

func TestSendNon2xxIsRecordedNotErrored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	res := NewClient(time.Second).Send(context.Background(), Request{URL: srv.URL})
	if res.Err != nil || res.StatusCode != 500 || res.OK() {
		t.Fatalf("got %+v", res)
	}
}

func TestSendDoesNotFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()

	res := NewClient(time.Second).Send(context.Background(), Request{URL: srv.URL})
	if res.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", res.StatusCode)
	}
}

func TestSendTimeoutIsRecorded(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	res := NewClient(50 * time.Millisecond).Send(context.Background(), Request{URL: srv.URL})
	if res.Err == nil || res.StatusCode != 0 || res.OK() {
		t.Fatalf("expected timeout, got %+v", res)
	}
}

func TestSendConnectionRefusedIsRecorded(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	res := NewClient(time.Second).Send(context.Background(), Request{URL: url})
	if res.Err == nil || res.OK() {
		t.Fatalf("expected connection error, got %+v", res)
	}
}

func TestSendTruncatesLargeBodies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", MaxBodyBytes+100)))
	}))
	defer srv.Close()

	res := NewClient(time.Second).Send(context.Background(), Request{URL: srv.URL})
	if !res.Truncated || len(res.Body) != MaxBodyBytes || res.Err != nil {
		t.Fatalf("truncated=%v len=%d err=%v", res.Truncated, len(res.Body), res.Err)
	}
}
