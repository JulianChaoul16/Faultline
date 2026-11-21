package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type Event struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

func main() {
	http.HandleFunc("/webhook", func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var event Event

		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		fmt.Println("Received:", event.ID, event.Type)

		w.WriteHeader(http.StatusOK)
	})

	fmt.Println("Listening on localhost:8080")
	http.ListenAndServe("127.0.0.1:8080", nil)
}
