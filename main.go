package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// maxBodyBytes caps a request body. Every request this API accepts is a few
// short fields; without a cap a client could stream an unbounded body into the
// JSON decoder. Same value and helper as pubvera-grantvera.
const maxBodyBytes = 64 << 10

// Ceilings for the numeric fields. The values come from the frontend slider
// maxima in index.html (firmLimit 50, recentDays 365, recentLimit 50): nothing
// the page can send exceeds them, so a larger value is rejected, never clamped.
const (
	maxFirmLimit   = 50
	maxRecentLimit = 50
	maxRecentDays  = 365
)

// decodeJSONRequest is the one input boundary for every handler: it caps the
// body size (413), rejects unknown fields so a typo cannot silently fall back
// to a default, and rejects trailing data after the object (both 400).
func decodeJSONRequest(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return false
		}
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return false
	}
	if err := dec.Decode(new(struct{})); err != io.EOF {
		http.Error(w, "invalid JSON: trailing data after the request object", http.StatusBadRequest)
		return false
	}
	return true
}

// validateTextArg trims a free-text field in place and rejects it when empty or
// when it starts with '-'. The value is passed to the CLI as a positional
// argument, and the CLI's flag parser treats it as one only because it does not
// start with a dash: "--help" or "-json" would otherwise be read as a flag,
// still spawn a process, hold a CLI slot, and come back as a 502.
func validateTextArg(w http.ResponseWriter, name string, v *string) bool {
	*v = strings.TrimSpace(*v)
	if *v == "" {
		http.Error(w, "missing "+name, http.StatusBadRequest)
		return false
	}
	if strings.HasPrefix(*v, "-") {
		http.Error(w, name+" must not start with '-'", http.StatusBadRequest)
		return false
	}
	return true
}

// checkCeiling rejects a numeric field above its ceiling with a 400 naming the
// field. It runs before defaults are applied; 0 and negatives pass through to
// the default so an empty slider (JSON null → 0) keeps working.
func checkCeiling(w http.ResponseWriter, name string, v, max int) bool {
	if v > max {
		http.Error(w, fmt.Sprintf("%s must be at most %d", name, max), http.StatusBadRequest)
		return false
	}
	return true
}

// POST /api/check
type checkRequest struct {
	Drug  string `json:"drug"`
	Class string `json:"class,omitempty"`
}

// cliClassArg translates the recall class as the PAGE names it into the value
// the CLI accepts. The two sides use different vocabularies: the <select> in
// index.html sends the Roman numerals "I", "II" and "III", while the CLI
// declares `--class int` (1=Class I, 2=Class II, 3=Class III). Passing the page
// value straight through made the CLI exit 2 with
// `invalid argument "I" for "--class" flag`, so every class selection returned
// 502 and only "Any class" ever worked. This function is the one place the two
// vocabularies meet, so it accepts both and nothing else.
//
// The empty string means "any class" and maps to an empty result: the caller
// omits the flag entirely. ok is false for anything that is neither.
func cliClassArg(class string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(class)) {
	case "":
		return "", true
	case "I", "1":
		return "1", true
	case "II", "2":
		return "2", true
	case "III", "3":
		return "3", true
	default:
		return "", false
	}
}

func handleCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "only POST", http.StatusMethodNotAllowed)
		return
	}
	var req checkRequest
	if !decodeJSONRequest(w, r, &req) {
		return
	}
	if !validateTextArg(w, "drug", &req.Drug) {
		return
	}

	class, ok := cliClassArg(req.Class)
	if !ok {
		http.Error(w, "class must be one of I, II, III", http.StatusBadRequest)
		return
	}

	args := []string{"check", req.Drug}
	if class != "" {
		args = append(args, "--class", class)
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
	if !decodeJSONRequest(w, r, &req) {
		return
	}
	if !validateTextArg(w, "firm", &req.Firm) {
		return
	}
	if !checkCeiling(w, "limit", req.Limit, maxFirmLimit) {
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
	if !decodeJSONRequest(w, r, &req) {
		return
	}
	if !checkCeiling(w, "days", req.Days, maxRecentDays) || !checkCeiling(w, "limit", req.Limit, maxRecentLimit) {
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
	if !decodeJSONRequest(w, r, &req) {
		return
	}
	if !validateTextArg(w, "recall_number", &req.RecallNumber) {
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
