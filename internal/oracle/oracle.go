// Package oracle asks the receiver's world how many times an event was
// processed, by running a user-supplied shell command.
//
// Faultline can only see HTTP responses, and a receiver that processes a
// duplicate twice still answers 200 both times. The oracle is the ground
// truth: a command such as
//
//	curl -s "http://localhost:8080/count?id={{id}}"
//
// that prints a single integer. Comparing that integer with the expected
// count is left to the caller.
//
// Any error from Count means the check itself could not produce an answer
// (bad output, non-zero exit, timeout). Callers must report that as
// inconclusive, never as a receiver failure.
package oracle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// Placeholder in the command template that is replaced by the event ID.
	Placeholder = "{{id}}"
	// EnvVar holds the event ID in the command's environment, for commands
	// that would rather not use the template.
	EnvVar = "FAULTLINE_ID"
	// MaxOutputBytes caps how much stdout/stderr is kept for the report.
	MaxOutputBytes = 4 << 10
)

// waitDelay bounds how long we wait for output pipes to close after the
// shell is killed on timeout. Without it, a grandchild process (e.g. curl
// started by sh) that inherited the pipes could keep Count blocked.
const waitDelay = 500 * time.Millisecond

// validID restricts IDs to characters that are inert in every shell, so
// substituting one into the command line cannot inject anything.
var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Command is a check command template.
type Command struct {
	Template string        // shell command line containing {{id}} (optional)
	Timeout  time.Duration // zero means no timeout beyond ctx
}

// Observation is what one run of the check command produced.
type Observation struct {
	Count  int    // valid only when Count returned a nil error
	Stdout string // at most MaxOutputBytes
	Stderr string // at most MaxOutputBytes
}

// Count runs the check for event id and parses its stdout as a count.
// The Observation is returned even on error so the report can show what
// the command actually printed.
func (c Command) Count(ctx context.Context, id string) (Observation, error) {
	if !validID.MatchString(id) {
		return Observation{}, fmt.Errorf("oracle: event id %q must be 1-128 characters of A-Z a-z 0-9 _ -", id)
	}
	if strings.TrimSpace(c.Template) == "" {
		return Observation{}, errors.New("oracle: empty check command")
	}

	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}

	line := strings.ReplaceAll(c.Template, Placeholder, id)
	cmd := shellCommand(ctx, line)
	cmd.Env = append(os.Environ(), EnvVar+"="+id)
	cmd.WaitDelay = waitDelay

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	obs := Observation{
		Stdout: truncate(stdout.String()),
		Stderr: truncate(stderr.String()),
	}

	switch {
	case ctx.Err() == context.DeadlineExceeded:
		return obs, fmt.Errorf("oracle: check command timed out after %s", c.Timeout)
	case ctx.Err() != nil:
		return obs, fmt.Errorf("oracle: check command cancelled: %w", ctx.Err())
	case runErr != nil:
		return obs, fmt.Errorf("oracle: check command failed: %w", runErr)
	}

	out := strings.TrimSpace(stdout.String())
	n, err := strconv.Atoi(out)
	if err != nil {
		return obs, fmt.Errorf("oracle: check command must print one integer, got %q", truncate(out))
	}
	if n < 0 {
		return obs, fmt.Errorf("oracle: check command printed negative count %d", n)
	}
	obs.Count = n
	return obs, nil
}

func truncate(s string) string {
	if len(s) > MaxOutputBytes {
		return s[:MaxOutputBytes] + "...(truncated)"
	}
	return s
}
