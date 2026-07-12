package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8094"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/check", handleCheck)
	mux.HandleFunc("/api/firm", handleFirm)
	mux.HandleFunc("/api/recent", handleRecent)
	mux.HandleFunc("/api/reference", handleReference)
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("/", handleRoot)

	srv := &http.Server{
		Addr:              "0.0.0.0:" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("drug-enforcement-web listening on 0.0.0.0:%s (CLI=%s)", port, cliBinary())
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, "index.html")
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func cliBinary() string {
	if b := os.Getenv("CLI_BIN"); b != "" {
		return b
	}
	return "./drug-enforcement-pp-cli"
}

func runCLI(args ...string) ([]byte, error) {
	bin := cliBinary()
	cmd := exec.Command(bin, args...)
	var out []byte
	var err error
	out, err = cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("CLI error: %v", err)
	}
	return out, nil
}

func writeRaw(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

// POST /api/check
type checkRequest struct {
	Drug  string `json:"drug"`
	Class string `json:"class,omitempty"`
}

func handleCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "only POST", http.StatusMethodNotAllowed)
		return
	}
	var req checkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Drug == "" {
		http.Error(w, "missing drug", http.StatusBadRequest)
		return
	}

	args := []string{"check", req.Drug}
	if req.Class != "" {
		args = append(args, "--class", req.Class)
	}
	args = append(args, "--json")

	out, err := runCLI(args...)
	if err != nil {
		log.Print(err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeRaw(w, out)
}

// POST /api/firm
type firmRequest struct {
	Firm  string `json:"firm"`
	Limit int    `json:"limit,omitempty"`
}

func handleFirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "only POST", http.StatusMethodNotAllowed)
		return
	}
	var req firmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Firm == "" {
		http.Error(w, "missing firm", http.StatusBadRequest)
		return
	}
	if req.Limit <= 0 {
		req.Limit = 15
	}

	args := []string{"firm", req.Firm, "--limit", fmt.Sprintf("%d", req.Limit), "--json"}

	out, err := runCLI(args...)
	if err != nil {
		log.Print(err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeRaw(w, out)
}

// POST /api/recent
type recentRequest struct {
	Days  int `json:"days,omitempty"`
	Limit int `json:"limit,omitempty"`
}

func handleRecent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "only POST", http.StatusMethodNotAllowed)
		return
	}
	var req recentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Days <= 0 {
		req.Days = 30
	}
	if req.Limit <= 0 {
		req.Limit = 15
	}

	args := []string{"recent", "--days", fmt.Sprintf("%d", req.Days), "--limit", fmt.Sprintf("%d", req.Limit), "--json"}

	out, err := runCLI(args...)
	if err != nil {
		log.Print(err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeRaw(w, out)
}

// POST /api/reference
type referenceRequest struct {
	RecallNumber string `json:"recall_number"`
}

func handleReference(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "only POST", http.StatusMethodNotAllowed)
		return
	}
	var req referenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.RecallNumber == "" {
		http.Error(w, "missing recall_number", http.StatusBadRequest)
		return
	}

	args := []string{"reference", req.RecallNumber, "--json"}

	out, err := runCLI(args...)
	if err != nil {
		log.Print(err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeRaw(w, out)
}
