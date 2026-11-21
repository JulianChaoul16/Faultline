# Faultline
Opensource Webhook Chaotic Reliability Testing

## Send a JSON webhook

The Go CLI in `sender.go` reads a local JSON file, sends it to the URL
with an HTTP POST, and prints the response status and body.

```sh
go run . send --url <url> --file <json-file>
```

To try it locally, start the receiver in one terminal.

```sh
go run reciever.go
```

Save the following as `payment.json`.

```json
{"id":"evt_1","type":"example"}
```

In another terminal, run the sender.

```sh
go run . send --url http://localhost:8080/webhook --file payment.json
```

The sender validates the JSON before sending it, uses a 30-second timeout,
and prints redirect responses directly. If you build it with
`go build .`, the executable exits with status 1 on errors or non-2xx
responses and status 2 for invalid command-line arguments.
