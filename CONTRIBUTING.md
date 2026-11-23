# Contributing to Faultline

Thanks for helping. Bug reports, ideas, docs fixes and code are all
welcome.

## Ways to contribute

- **Report a bug.** Open an issue with the bug report template. Include the
  exact `faultline` command, the output, and what you expected.
- **Suggest a scenario.** Faultline grows by adding new kinds of delivery
  chaos. Open an issue describing the failure it would catch in a real
  receiver.
- **Send a pull request.** For anything larger than a small fix, open an
  issue first so we can agree on the approach before you write code.

Issues labelled `good first issue` are a good place to start.

## Development setup

You need Go 1.22 or newer and `curl` on your `PATH`, which the end-to-end
tests use as a check command.

```sh
git clone https://github.com/JulianChaoul16/Faultline.git
cd Faultline
go test ./...
```

Before you open a pull request, run the same checks CI runs.

```sh
gofmt -l .      # must print nothing
go vet ./...
go test ./...
```

To try your change by hand, start an example receiver in one terminal and
run Faultline against it from another.

```sh
go run ./examples/receivers/racy
go run ./cmd/faultline --url http://127.0.0.1:8080/webhook \
  --check 'curl -s "http://127.0.0.1:8080/count?id={{id}}"'
```

On Windows with Smart App Control, Go test binaries are sometimes blocked
with *"An Application Control policy has blocked this file."* That is the
OS, not your change. Re-run the tests.

## How the code is organised

| Package | Job |
|---|---|
| `internal/delivery` | send one webhook, record what happened |
| `internal/oracle` | run the `--check` command, parse the count |
| `internal/scenario` | build the event, send the copies, decide the verdict |
| `internal/report` | print results, pick the exit code |
| `cmd/faultline` | flags and wiring |
| `examples/receivers` | `naive`, `racy` and `correct` receivers to test against |

## Design rules

Please keep these in mind. Reviews will check for them.

1. **A misbehaving receiver is an observation, not an error.** Timeouts,
   refused connections and 5xx responses go into a `Result` or `Outcome`.
   Go `error` values are for Faultline's own setup problems.
2. **Never blame the receiver for a broken check.** If the oracle cannot
   give an answer, the verdict is INCONCLUSIVE (exit 3), never FAIL.
3. **No false alarms.** Every new scenario must PASS against
   `examples/receivers/correct`. If it needs a new kind of bug to catch,
   add an example receiver that has that bug.
4. **Validate before sending.** Configuration mistakes exit with code 2
   before any request goes out.
5. **No new dependencies without discussion.** Faultline uses only the Go
   standard library today.

## Adding a scenario

1. Open an issue describing the real-world bug the scenario catches.
2. Add it in `internal/scenario` with tests in the verdict table in
   `scenario_test.go`, covering at least one receiver that should FAIL and
   one that should PASS.
3. Document it in the README's Scenarios table.

## Pull requests

- Keep each pull request to one change; small ones get reviewed faster.
- Add or update tests for any behaviour change. A bug fix should include a
  test that fails without the fix.
- Update the README when flags, output or exit codes change.
- Write commit messages that say what changed and why.

## License

Faultline is released under the [MIT License](LICENSE). By submitting a
contribution, you agree that it is licensed under the same terms.

## Code of conduct

This project follows the [Code of Conduct](CODE_OF_CONDUCT.md). By taking
part, you agree to uphold it.
