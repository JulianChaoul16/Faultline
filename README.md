# Faultline
Opensource Webhook Chaotic Reliability Testing

## Send a JSON webhook

The Go CLI in `sender.go` reads a local JSON file, sends it to the URL
with an HTTP POST, and prints the response status and body.

```sh
go run sender.go <url> <json-file>
```

To try it locally, start the receiver in one terminal.

```sh
go run reciever.go
```

Save the following as `event.json`.

```json
{"id":"evt_1","type":"example"}
```

In another terminal, run the sender.

```sh
go run sender.go http://127.0.0.1:8080/webhook event.json
```

The sender validates the JSON before sending it, uses a 30-second timeout,
and prints redirect responses directly. If you build it with
`go build sender.go`, the executable exits with status 1 on errors or
non-2xx responses and status 2 when the arguments are missing or extra.
