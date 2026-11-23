package scenario

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"faultline/internal/delivery"
)

// Target describes the receiver and the event to send it.
type Target struct {
	URL    string
	Body   []byte      // JSON object used as a template for every run
	Header http.Header // extra headers sent with every delivery

	// IDField is the top-level JSON field that receives the fresh event ID.
	// Empty means "id". Set NoIDField to leave the body untouched.
	IDField   string
	NoIDField bool
	// IDHeader, if set, is a header that also carries the event ID
	// (for receivers that deduplicate on a header like Idempotency-Key).
	IDHeader string
}

// NewEventID returns a fresh random ID such as "flt_3f9a...".
//
// A new ID per scenario means earlier runs (or earlier scenarios in the
// same run) can never be mistaken for duplicates of this one. Its
// characters are also safe for the oracle's command substitution.
func NewEventID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("scenario: crypto/rand failed: " + err.Error())
	}
	return "flt_" + hex.EncodeToString(b[:])
}

// Request builds the delivery for event id: the body template with the ID
// written into IDField, plus the ID header if configured.
//
// The body is re-encoded, so key order and whitespace may differ from the
// template. Receivers that verify signatures over raw bytes need signing
// support, which comes later.
func (t Target) Request(id string) (delivery.Request, error) {
	body := t.Body
	if !t.NoIDField {
		field := t.IDField
		if field == "" {
			field = "id"
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(t.Body, &obj); err != nil || obj == nil {
			return delivery.Request{}, errors.New("scenario: event body must be a JSON object")
		}
		obj[field], _ = json.Marshal(id) // marshalling a string cannot fail
		var err error
		if body, err = json.Marshal(obj); err != nil {
			return delivery.Request{}, fmt.Errorf("scenario: encode event body: %w", err)
		}
	}

	header := t.Header.Clone()
	if t.IDHeader != "" {
		if header == nil {
			header = http.Header{}
		}
		header.Set(t.IDHeader, id)
	}
	return delivery.Request{URL: t.URL, Body: body, Header: header}, nil
}
