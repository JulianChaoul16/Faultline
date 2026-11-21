package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "Usage: sender <url> <json-file>")
		os.Exit(2)
	}
	if err := postJSON(os.Args[1], os.Args[2]); err != nil {
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
