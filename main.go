package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

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
	go janitor()

	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))
	
	http.HandleFunc("/", handleHome)
	http.HandleFunc("/action/dump", handleDumpAction)
	http.HandleFunc("/events", handleEvents)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("Server starting on port %s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// Helper to generate a random session ID
func generateSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Get or set the session cookie
func getOrCreateSession(w http.ResponseWriter, r *http.Request) string {
	cookie, err := r.Cookie("whoelse_session")
	if err == nil {
		return cookie.Value
	}

	// No cookie found, create a new one
	sessionID := generateSessionID()
	http.SetCookie(w, &http.SetCookie{
		Name:     "whoelse_session",
		Value:    sessionID,
		Path:     "/",
		Expires:  time.Now().Add(24 * time.Hour),
		HttpOnly: true, // Security best practice
		SameSite: http.SameSiteLaxMode,
	})
	return sessionID
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	// Ensure they get a session cookie just by visiting the page
	getOrCreateSession(w, r)
	http.ServeFile(w, r, "./static/index.html")
}

func handleDumpAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Identify the user by their unique cookie instead of IP
	sessionID := getOrCreateSession(w, r)

	state.Lock()
	state.ActiveDumpers[sessionID] = time.Now().Add(10 * time.Minute)
	currentCount := len(state.ActiveDumpers)
	state.Unlock()

	broadcast(currentCount)
	w.WriteHeader(http.StatusOK)
}

func handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	messageChan := make(chan int)

	state.Lock()
	state.Clients[messageChan] = true
	initialCount := len(state.ActiveDumpers)
	state.Unlock()

	fmt.Fprintf(w, "event: dump_update\ndata: %d\n\n", initialCount)
	w.(http.Flusher).Flush()

	for {
		select {
		case count := <-messageChan:
			fmt.Fprintf(w, "event: dump_update\ndata: %d\n\n", count)
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			state.Lock()
			delete(state.Clients, messageChan)
			close(messageChan)
			state.Unlock()
			return
		}
	}
}

func broadcast(count int) {
	state.Lock()
	defer state.Unlock()
	for clientChan := range state.Clients {
		clientChan <- count
	}
}

func janitor() {
	for {
		time.Sleep(10 * time.Second)

		state.Lock()
		now := time.Now()
		changed := false

		for sessionID, expiry := range state.ActiveDumpers {
			if now.After(expiry) {
				delete(state.ActiveDumpers, sessionID)
				changed = true
			}
		}

		currentCount := len(state.ActiveDumpers)
		state.Unlock()

		if changed {
			broadcast(currentCount)
		}
	}
}