package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	send := flag.NewFlagSet("send", flag.ExitOnError)
	url := send.String("url", "", "destination URL")
	filename := send.String("file", "", "path to the JSON file")
	send.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: faultline send --url <url> --file <json-file>")
		send.PrintDefaults()
	}

	if len(os.Args) < 2 || os.Args[1] != "send" {
		send.Usage()
		os.Exit(2)
	}
	send.Parse(os.Args[2:])
	if *url == "" || *filename == "" || send.NArg() != 0 {
		send.Usage()
		os.Exit(2)
	}

	// PowerShell passes a backslash before the scheme colon through literally.
	destination := *url
	scheme, rest, found := strings.Cut(destination, "://")
	if found && (scheme == `http\` || scheme == `https\`) {
		destination = strings.TrimSuffix(scheme, `\`) + "://" + rest
	}
	if err := postJSON(destination, *filename); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func postJSON(url, filename string) error {
	payload, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	if !json.Valid(payload) {
		return fmt.Errorf("%s: invalid JSON", filename)
	}

	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if _, err := fmt.Fprintln(os.Stdout, response.Status); err != nil {
		return err
	}
	if _, err := io.Copy(os.Stdout, response.Body); err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("POST returned HTTP %d", response.StatusCode)
	}
	return nil
}
