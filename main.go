package main

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// Global state to track who is currently "active"
type AppState struct {
	sync.Mutex
	ActiveDumpers map[string]time.Time
	Clients       map[chan int]bool
}

var state = AppState{
	ActiveDumpers: make(map[string]time.Time),
	Clients:       make(map[chan int]bool),
}

func main() {
	// Start the background worker to clean up inactive users
	go janitor()

	// Serve static files (HTML/CSS)
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))
	
	// Route for the homepage
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./static/index.html")
	})

	// Route when someone clicks the button
	http.HandleFunc("/action/dump", handleDumpAction)

	// Route for the real-time SSE stream
	http.HandleFunc("/events", handleEvents)

	fmt.Println("Server starting on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// 1. Handle the button click
func handleDumpAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state.Lock()
	// Use the remote IP address as a simple unique identifier
	ip := r.RemoteAddr
	// Set an expiration time 10 minutes from now
	state.ActiveDumpers[ip] = time.Now().Add(10 * time.Minute)
	currentCount := len(state.ActiveDumpers)
	state.Unlock()

	// Broadcast the new count to everyone immediately
	broadcast(currentCount)

	// HTMX expects a response. Since hx-swap="none", we return nothing.
	w.WriteHeader(http.StatusOK)
}

// 2. Handle the Live Event Stream (SSE)
func handleEvents(w http.ResponseWriter, r *http.Request) {
	// Set headers required for Server-Sent Events
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Create a channel for this specific browser tab
	messageChan := make(chan int)

	state.Lock()
	state.Clients[messageChan] = true
	// Send the current count immediately upon connecting
	initialCount := len(state.ActiveDumpers)
	state.Unlock()

	// Send initial count
	fmt.Fprintf(w, "event: dump_update\ndata: %d\n\n", initialCount)
	w.(http.Flusher).Flush()

	// Keep connection open and listen for updates or disconnects
	for {
		select {
		case count := <-messageChan:
			// Push the updated count to the browser
			fmt.Fprintf(w, "event: dump_update\ndata: %d\n\n", count)
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			// Browser closed the tab
			state.Lock()
			delete(state.Clients, messageChan)
			close(messageChan)
			state.Unlock()
			return
		}
	}
}

// 3. Helper to send updates to all connected browser tabs
func broadcast(count int) {
	state.Lock()
	defer state.Unlock()
	for clientChan := range state.Clients {
		clientChan <- count
	}
}

// 4. The Janitor: Runs in background, boots people out after 10 minutes
func janitor() {
	for {
		time.Sleep(10 * time.Second) // Check every 10 seconds

		state.Lock()
		now := time.Now()
		changed := false

		for ip, expiry := range state.ActiveDumpers {
			if now.After(expiry) {
				delete(state.ActiveDumpers, ip)
				changed = true
			}
		}

		currentCount := len(state.ActiveDumpers)
		state.Unlock()

		// If someone expired, tell everyone the count went down
		if changed {
			broadcast(currentCount)
		}
	}
}
