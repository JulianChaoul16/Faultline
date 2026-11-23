// Command faultline tests a webhook receiver against duplicate deliveries.
//
//	faultline --url http://localhost:8080/webhook \
//	          --check 'curl -s "http://localhost:8080/count?id={{id}}"'
//
// It sends the same event several times (one after another, then all at
// once), runs the check command to ask how many times the receiver really
// processed it, and reports PASS / FAIL / INCONCLUSIVE.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	"faultline/internal/delivery"
	"faultline/internal/oracle"
	"faultline/internal/report"
	"faultline/internal/scenario"
)

const defaultBody = `{"type":"faultline.test"}`

func main() {
	// Ctrl+C cancels in-flight deliveries; what already ran is still reported.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// options is the parsed command line.
type options struct {
	url          string
	bodyFile     string
	check        string
	expect       int
	idField      string
	idHeader     string
	headers      headerFlag
	scenarios    string
	timeout      time.Duration
	checkTimeout time.Duration
	verbose      bool
}

// run is main without the process-level parts, so tests can call it.
// It returns the exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opts, err := parseFlags(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return report.ExitPass
	}
	if err != nil {
		fmt.Fprintln(stderr, "faultline:", err)
		return report.ExitUsage
	}

	cfg, scenarios, err := configure(opts)
	if err != nil {
		fmt.Fprintln(stderr, "faultline:", err)
		return report.ExitUsage
	}

	fmt.Fprintf(stdout, "faultline: %d scenario(s) against %s\n\n", len(scenarios), cfg.Target.URL)
	var outcomes []scenario.Outcome
	for _, s := range scenarios {
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "faultline: interrupted")
			break
		}
		out, err := scenario.Run(ctx, cfg, s)
		if err != nil { // configuration problem; configure should have caught it
			fmt.Fprintln(stderr, "faultline:", err)
			return report.ExitUsage
		}
		outcomes = append(outcomes, out)
	}

	if err := report.Write(stdout, outcomes, opts.verbose); err != nil {
		fmt.Fprintln(stderr, "faultline: writing report:", err)
	}
	if ctx.Err() != nil && len(outcomes) < len(scenarios) {
		return report.ExitInconclusive
	}
	return report.Summarize(outcomes).ExitCode()
}

func parseFlags(args []string, stderr io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("faultline", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.url, "url", "", "receiver URL to POST webhooks to (required)")
	fs.StringVar(&o.check, "check", "", "shell command that prints how many times event {{id}} was processed (required)")
	fs.StringVar(&o.bodyFile, "body", "", "JSON file used as the event body (default "+defaultBody+")")
	fs.IntVar(&o.expect, "expect", 1, "how many times a correct receiver processes one event")
	fs.StringVar(&o.idField, "id-field", "id", `top-level JSON field to put the fresh event ID in; "" to leave the body alone`)
	fs.StringVar(&o.idHeader, "id-header", "", "header to also carry the event ID, e.g. Idempotency-Key")
	fs.Var(&o.headers, "header", `extra header "Name: value" (repeatable)`)
	fs.StringVar(&o.scenarios, "scenario", "", "comma-separated scenarios to run (default all: "+scenarioNames()+")")
	fs.DurationVar(&o.timeout, "timeout", 10*time.Second, "timeout for each delivery")
	fs.DurationVar(&o.checkTimeout, "check-timeout", 10*time.Second, "timeout for the check command")
	fs.BoolVar(&o.verbose, "v", false, "show details for passing scenarios too")
	fs.Usage = func() {
		fmt.Fprint(stderr, `Usage: faultline --url <receiver> --check '<command with {{id}}>' [flags]

Sends the same webhook event several times and checks, with your command,
that the receiver processed it exactly --expect times.

Example:
  faultline --url http://localhost:8080/webhook \
            --check 'curl -s "http://localhost:8080/count?id={{id}}"'

Exit codes: 0 pass, 1 receiver bug found, 2 usage error, 3 inconclusive.

Flags:
`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return o, err
	}
	switch {
	case fs.NArg() != 0:
		return o, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	case o.url == "":
		return o, errors.New("--url is required")
	case o.check == "":
		return o, errors.New("--check is required")
	case o.expect < 0:
		return o, errors.New("--expect must be 0 or more")
	case o.timeout <= 0 || o.checkTimeout <= 0:
		return o, errors.New("timeouts must be positive")
	}
	return o, nil
}

// configure turns options into a scenario config, validating everything
// up front so no delivery is sent with a setup mistake.
func configure(o options) (scenario.Config, []scenario.Scenario, error) {
	target, err := parseURL(o.url)
	if err != nil {
		return scenario.Config{}, nil, err
	}

	body := []byte(defaultBody)
	if o.bodyFile != "" {
		if body, err = os.ReadFile(o.bodyFile); err != nil {
			return scenario.Config{}, nil, err
		}
	}

	t := scenario.Target{
		URL:       target,
		Body:      body,
		Header:    http.Header(o.headers),
		IDField:   o.idField,
		NoIDField: o.idField == "",
		IDHeader:  o.idHeader,
	}
	if t.NoIDField && t.IDHeader == "" {
		return scenario.Config{}, nil, errors.New(`--id-field "" needs --id-header, or every run would send the same event`)
	}
	if _, err := t.Request(scenario.NewEventID()); err != nil {
		return scenario.Config{}, nil, fmt.Errorf("%s: %w", bodyName(o.bodyFile), err)
	}

	scenarios, err := pickScenarios(o.scenarios)
	if err != nil {
		return scenario.Config{}, nil, err
	}

	return scenario.Config{
		Target: t,
		Client: delivery.NewClient(o.timeout),
		Oracle: oracle.Command{Template: o.check, Timeout: o.checkTimeout},
		Expect: o.expect,
	}, scenarios, nil
}

func parseURL(raw string) (string, error) {
	// Windows PowerShell sometimes passes "http\://" through literally.
	if scheme, rest, ok := strings.Cut(raw, "://"); ok && (scheme == `http\` || scheme == `https\`) {
		raw = strings.TrimSuffix(scheme, `\`) + "://" + rest
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("--url %q must be an http:// or https:// URL", raw)
	}
	return u.String(), nil
}

func pickScenarios(list string) ([]scenario.Scenario, error) {
	if strings.TrimSpace(list) == "" {
		return scenario.Defaults, nil
	}
	var picked []scenario.Scenario
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		found := false
		for _, s := range scenario.Defaults {
			if s.Name == name {
				picked = append(picked, s)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown scenario %q (have: %s)", name, scenarioNames())
		}
	}
	return picked, nil
}

func scenarioNames() string {
	names := make([]string, len(scenario.Defaults))
	for i, s := range scenario.Defaults {
		names[i] = s.Name
	}
	return strings.Join(names, ", ")
}

func bodyName(file string) string {
	if file == "" {
		return "default body"
	}
	return file
}

// headerFlag collects repeated --header "Name: value" flags.
type headerFlag http.Header

func (h *headerFlag) String() string { return fmt.Sprint(http.Header(*h)) }

func (h *headerFlag) Set(v string) error {
	name, value, ok := strings.Cut(v, ":")
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return fmt.Errorf("header %q must look like \"Name: value\"", v)
	}
	if *h == nil {
		*h = headerFlag{}
	}
	http.Header(*h).Add(name, strings.TrimSpace(value))
	return nil
}
