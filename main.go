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

// cliRunTimeout is the upper bound on a child process run. There was NO limit
// at all before this: a stuck CLI ran until the parent process exited.
const cliRunTimeout = 120 * time.Second

// Server-side timeouts. ReadHeaderTimeout was the only one set, which left the
// request BODY with no deadline at all: a size limit is not a time limit, and a
// client that sends its body one byte per minute holds a handler goroutine for
// as long as it likes. Caddy fronts this app in production and sets no request
// timeout of its own, so this is the only place the limit exists.
//
// WriteTimeout is the one that must not be guessed. It covers the whole
// response, and a CLI run is allowed cliRunTimeout to produce it, so anything
// at or below cliRunTimeout would cut off legitimate slow lookups rather than
// attacks — and only the slowest ones, intermittently, which is far harder to
// diagnose than the exposure being closed. It is derived from cliRunTimeout so
// that a change to the CLI budget carries here instead of silently leaving this
// too short.
const (
	srvReadHeaderTimeout = 10 * time.Second
	// The body is a small JSON object. Thirty seconds is far more than a real
	// client needs and far less than a slow-loris attacker wants.
	srvReadTimeout = 30 * time.Second
	// The full CLI budget plus room to write the response.
	srvWriteTimeout = cliRunTimeout + 30*time.Second
	// Keep-alive connections that go quiet are released rather than held.
	srvIdleTimeout = 120 * time.Second
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
		ReadHeaderTimeout: srvReadHeaderTimeout,
		ReadTimeout:       srvReadTimeout,
		WriteTimeout:      srvWriteTimeout,
		IdleTimeout:       srvIdleTimeout,
	}

	log.Printf("recallis listening on 0.0.0.0:%s (CLI=%s, timeout=%s, slots=%d)",
		port, cliBinary(), cliRunTimeout, cliSem.capacity())
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// browserConfig is the bootstrap payload /config.json hands to the page so it
// can build its Supabase client. SupabaseAnonKey is the PUBLISHABLE
// (browser-side) key, never the secret one: it is designed to be visible in a
// browser and Row Level Security is what protects the data. It is still never
// logged.
type browserConfig struct {
	SupabaseURL     string `json:"supabase_url"`
	SupabaseAnonKey string `json:"supabase_anon_key"`
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/config.json":
		// Deliberately NOT under /api/: Caddy protects /api/* with forward_auth,
		// and the page needs this config BEFORE it can sign anyone in. Serving it
		// from a protected path would make the requirement circular and force a
		// special-case exception into the Caddy matcher.
		//
		// A missing variable is not an error. An empty pair with status 200 is a
		// valid answer that puts the page into unauthenticated mode, which is what
		// keeps local development and the current deployment working until the
		// environment is set.
		supaURL := strings.TrimSpace(os.Getenv("SUPABASE_URL"))
		supaKey := strings.TrimSpace(os.Getenv("SUPABASE_PUBLISHABLE_KEY"))
		if supaURL == "" || supaKey == "" {
			supaURL, supaKey = "", ""
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		// Never cache: a stale key surviving a key rotation would be hard to
		// diagnose from the browser side.
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(browserConfig{SupabaseURL: supaURL, SupabaseAnonKey: supaKey})
	case "/":
		http.ServeFile(w, r, "index.html")
	default:
		http.NotFound(w, r)
	}
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
	// for the reader. This app is keyless, so there is no BYOK secret to redact
	// and stderr can go to the log verbatim, capped so a chatty CLI cannot
	// flood it.
	//
	// It does NOT go to the client. What the CLI writes on failure is its own
	// usage text, its version banner and raw upstream messages, none of it
	// bounded. That is operator information: it belongs in the log, not in an
	// HTTP response body.
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
		msg := strings.TrimSpace(stderr.String())
		log.Printf("cli: fail cmd=%s wait_ms=%d elapsed_ms=%d err=%v stderr=%s", label, waitMS, elapsed, err, truncate(msg, 2000))
		return nil, fmt.Errorf("CLI error: %v", err)
	}
	// A successful run can still have written to stderr, and those messages are
	// the ones worth seeing: the CLI prints its rate-limit and server-error
	// retries there while the command goes on to succeed. Logging stderr only on
	// failure discarded exactly the warnings that explain a slow but successful
	// request. Operator information, never sent to the client.
	if w := strings.TrimSpace(stderr.String()); w != "" {
		log.Printf("cli: ok cmd=%s wait_ms=%d elapsed_ms=%d bytes=%d stderr=%s", label, waitMS, elapsed, stdout.Len(), truncate(w, 300))
	} else {
		log.Printf("cli: ok cmd=%s wait_ms=%d elapsed_ms=%d bytes=%d", label, waitMS, elapsed, stdout.Len())
	}
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

// truncate caps a log line at max runes. Rune-based, not byte-based: a stderr
// message can carry UTF-8, and slicing bytes would split a character and put
// an invalid sequence in the log. Same shape as pubvera-corpova's.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "..."
}
