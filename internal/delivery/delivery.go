// Package delivery sends a single webhook attempt and records its outcome.
//
// A failed delivery (timeout, refused connection, 5xx) is not an error of
// Faultline itself; it is an observation about the receiver. Send therefore
// returns a Result in every case and never a separate error.
package delivery

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"
)

// MaxBodyBytes caps how much of a response body is kept in a Result.
const MaxBodyBytes = 64 << 10

// Request describes one webhook delivery.
type Request struct {
	URL    string
	Body   []byte
	Header http.Header // optional; Content-Type defaults to application/json
}

// Result is the observed outcome of one delivery attempt.
type Result struct {
	Start      time.Time
	Duration   time.Duration
	StatusCode int    // 0 if no response was received
	Body       []byte // at most MaxBodyBytes of the response body
	Truncated  bool   // true if the response body exceeded MaxBodyBytes
	Err        error  // transport error: timeout, connection refused, etc.
}

// OK reports whether the receiver acknowledged the delivery with a 2xx.
func (r Result) OK() bool {
	return r.Err == nil && r.StatusCode >= 200 && r.StatusCode < 300
}

// Client sends deliveries. It is safe for concurrent use.
type Client struct {
	http *http.Client
}

// NewClient returns a Client whose attempts time out after timeout.
// Redirects are not followed: a webhook receiver that redirects is
// reported as-is rather than silently chased.
func NewClient(timeout time.Duration) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Concurrent scenarios fire bursts at one host; keep those connections.
	transport.MaxIdleConnsPerHost = 64

	return &Client{http: &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// Send performs one POST and returns what happened.
//
// result is a named return value so the deferred function below can set
// Duration on the value actually returned; with an ordinary local
// variable, "return result" would copy it before the defer ran.
func (c *Client) Send(ctx context.Context, req Request) (result Result) {
	result.Start = time.Now()
	defer func() { result.Duration = time.Since(result.Start) }()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		result.Err = err
		return result
	}
	for key, values := range req.Header {
		httpReq.Header[key] = append([]string(nil), values...)
	}
	if httpReq.Header.Get("Content-Type") == "" {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		result.Err = err
		return result
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if len(body) > MaxBodyBytes {
		body = body[:MaxBodyBytes]
		result.Truncated = true
		// Drain the rest so the connection can be reused.
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	result.Body = body
	if err != nil {
		result.Err = err
	}
	return result
}
