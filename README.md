# Faultline
Opensource Webhook Chaotic Reliability Testing

[![CI](https://github.com/JulianChaoul16/Faultline/actions/workflows/ci.yml/badge.svg)](https://github.com/JulianChaoul16/Faultline/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Webhook senders such as Stripe and GitHub deliver events *at least once*,
which means your receiver will sometimes get the same event twice, or two
copies at the same moment. If it processes both, a customer gets charged
twice or an email goes out twice.

Faultline sends your receiver the same event several times and checks
whether it really processed the event only once.

## Quick start

You need Go 1.22 or newer. In one terminal, start an example receiver.

```sh
go run ./examples/receivers/naive
```

In another terminal, run Faultline against it.

```sh
go run ./cmd/faultline \
  --url http://127.0.0.1:8080/webhook \
  --check 'curl -s "http://127.0.0.1:8080/count?id={{id}}"'
```

On Windows PowerShell, use `curl.exe` instead of `curl`.

```
faultline: 2 scenario(s) against http://127.0.0.1:8080/webhook

FAIL         sequential-duplicates  processed 2 time(s), expected 1
             event flt_1430ba36642e8bdabec6b83f, 2 copies, sequential
             receiver answered: 2 × 200 OK (<1ms–<1ms)
FAIL         concurrent-duplicates  processed 10 time(s), expected 1
             event flt_33ea0f99e9e358bd297e5a43, 10 copies, concurrent
             receiver answered: 10 × 200 OK (<1ms–<1ms)

0 passed, 2 failed, 0 inconclusive
```

## How it works

Faultline runs each scenario in four steps.

1. It creates a fresh event ID, such as `flt_3f9a…`, and writes it into
   the event body.
2. It delivers the same event several times.
3. It runs your `--check` command, with `{{id}}` replaced by that ID, to
   ask how many times the receiver actually processed the event.
4. It compares the printed number with `--expect` (default `1`).

A receiver that answers `200 OK` twice looks healthy from the outside,
which is why Faultline needs the check command. The check is the source of
truth, often called the *oracle*. It can be any shell command that prints
one integer, such as a `curl` against a debug endpoint or a database query.

```sh
--check 'psql -tAc "select count(*) from payments where event_id = '\''{{id}}'\''"'
```

The ID is also available to the command as the `FAULTLINE_ID` environment
variable.

## Scenarios

| Scenario | What it does | What it catches |
|---|---|---|
| `sequential-duplicates` | sends 2 copies, one after another | no deduplication at all |
| `concurrent-duplicates` | sends 10 copies released at the same instant | a "check, then record" race, where simultaneous copies all pass the check |

## Results and exit codes

| Verdict | Meaning | Exit code |
|---|---|---|
| PASS | processed exactly `--expect` times | 0 |
| FAIL | processed a different number of times; a receiver bug | 1 |
| (usage error) | bad flags or config; nothing was sent | 2 |
| INCONCLUSIVE | the check command broke, or the receiver could not be reached | 3 |

If any scenario fails, the exit code is 1, even when others are
inconclusive.

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--url` | required | receiver URL to POST to |
| `--check` | required | command that prints the processed count for `{{id}}` |
| `--expect` | `1` | the correct count |
| `--body` | `{"type":"faultline.test"}` | JSON file used as the event body |
| `--id-field` | `id` | top-level body field for the fresh ID; `""` leaves the body untouched |
| `--id-header` | | also send the ID in this header, such as `Idempotency-Key` |
| `--header` | | extra `"Name: value"` header; repeatable |
| `--scenario` | all | comma-separated scenarios to run |
| `--timeout` | `10s` | timeout for each delivery |
| `--check-timeout` | `10s` | timeout for the check command |
| `-v` | off | show details for passing scenarios too |

## Example receivers

Each example listens on `127.0.0.1:8080` by default (change it with
`-addr`) and exposes `POST /webhook` and `GET /count?id=X`.

| Receiver | Behaviour | sequential | concurrent |
|---|---|---|---|
| `naive` | processes every delivery | FAIL | FAIL |
| `racy` | checks for duplicates, but not atomically | PASS | FAIL |
| `correct` | claims the event ID atomically before processing | PASS | PASS |

The `racy` and `correct` receivers take a `-work` flag (default `50ms`)
that sets how long processing takes. For `racy`, this is the window in
which duplicates slip through.

## Development

```sh
go test ./...
```

On Windows with Smart App Control, Go test binaries are sometimes blocked
with *"An Application Control policy has blocked this file."* That is the
OS, not a test failure. Re-run the tests, or set `GOTMPDIR` to another
folder.

## Layout

```
cmd/faultline/          the CLI: flags, wiring, exit codes
internal/delivery/      sends one webhook and records the outcome
internal/oracle/        runs the --check command and parses its count
internal/scenario/      builds events, sends duplicates, decides the verdict
internal/report/        prints results and picks the exit code
examples/receivers/     naive, racy and correct receivers to test against
```

## Contributing

Contributions are welcome, from bug reports to new scenarios. See
[CONTRIBUTING.md](CONTRIBUTING.md) for setup, design rules and how to send
a pull request. Please report security issues privately as described in
[SECURITY.md](SECURITY.md).

## License

Faultline is released under the [MIT License](LICENSE).
