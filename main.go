package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// cliRunTimeout a gyerekfolyamat felső határideje. Eddig SEMMILYEN nem volt:
// egy beragadt CLI a folyamat végéig futott.
const cliRunTimeout = 120 * time.Second

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

	log.Printf("recallis listening on 0.0.0.0:%s (CLI=%s, timeout=%s, slots=%d)",
		port, cliBinary(), cliRunTimeout, cliSem.capacity())
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

// cliCmdLabel names the subcommand for the log without leaking user input.
// Every caller in this file builds args with a fixed verb first and the user's
// value second (check <drug>, firm <firm>, recent --days N, reference <number>),
// so only the first element is safe to log.
func cliCmdLabel(args []string) string {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "?"
	}
	return args[0]
}

// runCLI runs the child CLI, bounded by both a concurrency slot and a deadline.
//
// The slot is acquired BEFORE the timeout starts. The other order would let a
// request spend most of its 120s budget queueing and then time out with the CLI
// barely started — failing for a reason that has nothing to do with the work.
//
// wait_ms is the only way to tell whether four slots is the right number: queue
// time is invisible in the CLI's own runtime, so without it a saturated
// semaphore and a slow upstream look identical from the outside. A sudden drop
// in bytes is the earliest sign of a quota or API failure — the run still
// succeeds and still looks fast, it just carries less back.
func runCLI(ctx context.Context, args ...string) ([]byte, error) {
	label := cliCmdLabel(args)
	waitStart := time.Now()
	err := cliSem.acquire(ctx)
	waitMS := time.Since(waitStart).Milliseconds()
	if err != nil {
		log.Printf("cli: busy cmd=%s wait_ms=%d err=%v", label, waitMS, err)
		return nil, err
	}
	defer cliSem.release()

	ctx, cancel := context.WithTimeout(ctx, cliRunTimeout)
	defer cancel()

	bin := cliBinary()
	cmd := exec.CommandContext(ctx, bin, args...)
	// stderr is captured separately: cmd.Output() discards it, so a CLI that
	// explains itself on stderr and exits non-zero left only "exit status 1"
	// for the reader. The message goes to the caller, never to the log — a
	// keyless request has no redaction in front of upstream text.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runStart := time.Now()
	err = cmd.Run()
	elapsed := time.Since(runStart).Milliseconds()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			log.Printf("cli: fail cmd=%s wait_ms=%d elapsed_ms=%d err=deadline", label, waitMS, elapsed)
			return nil, fmt.Errorf("CLI stopped after %s: %v", cliRunTimeout, ctxErr)
		}
		log.Printf("cli: fail cmd=%s wait_ms=%d elapsed_ms=%d err=%v", label, waitMS, elapsed, err)
		return nil, fmt.Errorf("CLI error: %v — stderr: %s", err, stderr.String())
	}
	log.Printf("cli: ok cmd=%s wait_ms=%d elapsed_ms=%d bytes=%d", label, waitMS, elapsed, stdout.Len())
	return stdout.Bytes(), nil
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

	out, err := runCLI(r.Context(), args...)
	if err != nil {
		log.Print(err)
		writeCLIError(w, err)
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

	out, err := runCLI(r.Context(), args...)
	if err != nil {
		log.Print(err)
		writeCLIError(w, err)
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

	out, err := runCLI(r.Context(), args...)
	if err != nil {
		log.Print(err)
		writeCLIError(w, err)
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

	out, err := runCLI(r.Context(), args...)
	if err != nil {
		log.Print(err)
		writeCLIError(w, err)
		return
	}
	writeRaw(w, out)
}
