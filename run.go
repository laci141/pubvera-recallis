package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// shutdownTimeout bounds the graceful drain. docker stop sends SIGKILL after
// 10 s, so the drain has to finish, and the key config has to be removed,
// before that.
const shutdownTimeout = 8 * time.Second

// runMain is main() with an error return: it wires the environment and the
// OS signals to run. SIGTERM (docker stop) and Ctrl-C cancel the context, which
// is what lets run's deferred cleanup execute; a bare defer in main() never ran
// on a signal, and log.Fatal skips defers by calling os.Exit.
func runMain() error {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8094"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", "0.0.0.0:"+port)
	if err != nil {
		return err
	}
	return run(ctx, ln, os.Getenv(openFDAKeyEnv))
}

// run serves on ln until ctx is cancelled or Serve fails. It owns ln.
func run(ctx context.Context, ln net.Listener, key string) error {
	purgeStaleConfigDirs()

	// Read once, here. An unreadable temp dir is fatal rather than a silent
	// fallback to keyless: an operator who set a key expects it to be used.
	cleanupKey, err := initOpenFDAKey(key)
	if err != nil {
		ln.Close()
		return fmt.Errorf("openfda key: %w", err)
	}
	defer cleanupKey()

	srv := &http.Server{
		Handler:           newMux(),
		ReadHeaderTimeout: srvReadHeaderTimeout,
		ReadTimeout:       srvReadTimeout,
		WriteTimeout:      srvWriteTimeout,
		IdleTimeout:       srvIdleTimeout,
	}

	_, port, _ := net.SplitHostPort(ln.Addr().String())
	log.Printf("recallis listening on 0.0.0.0:%s (CLI=%s, timeout=%s, slots=%d)",
		port, cliBinary(), cliRunTimeout, cliSem.capacity())

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	return nil
}

// purgeStaleConfigDirs removes recallis-openfda-* dirs left in the temp dir.
// There is one recallis process per container, so any such dir at startup
// belongs to a predecessor, and a crash or SIGKILL leaves it behind with the
// auth header still inside.
func purgeStaleConfigDirs() {
	dirs, err := filepath.Glob(filepath.Join(os.TempDir(), "recallis-openfda-*"))
	if err != nil {
		return
	}
	n := 0
	for _, d := range dirs {
		if os.RemoveAll(d) == nil {
			n++
		}
	}
	if n > 0 {
		log.Printf("openfda key: removed %d stale config dir(s)", n)
	}
}
