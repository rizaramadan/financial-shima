package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/web/handler"
	"github.com/rizaramadan/financial-shima/web/setup"
)

const (
	// defaultAddr is the listen address when ADDR is unset. ADDR accepts any
	// value valid for net.Listen("tcp", ...): ":8080", "127.0.0.1:8080",
	// "[::1]:8080".
	defaultAddr           = ":8080"
	shutdownGraceDuration = 10 * time.Second
)

func newServer() *echo.Echo {
	e := echo.New()
	setup.Apply(e)
	e.GET("/login", handler.LoginGet)
	return e
}

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = defaultAddr
	}
	// Syntactic validation only: SplitHostPort parses "host:port" with no
	// network I/O. (ResolveTCPAddr would do DNS for "myhost.local:8080".)
	if _, _, err := net.SplitHostPort(addr); err != nil {
		log.Fatalf("invalid ADDR %q (want host:port, e.g. \":8080\"): %v", addr, err)
	}

	e := newServer()

	// SIGTERM is the signal sent by systemd / Kubernetes for graceful
	// shutdown. On Windows, only os.Interrupt (Ctrl+C) is delivered;
	// SIGTERM is a no-op there. Production runs on Linux.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		defer close(serverErr)
		if err := e.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Print("shutdown signal received")
	case err := <-serverErr:
		// Bind failed (port in use, permission denied, etc.). Exit non-zero
		// so supervisors (systemd, k8s) treat it as a crash and restart.
		// log.Fatalf is correct here: there is nothing to drain; the only
		// pre-shutdown defer (signal stop) doesn't matter on process exit.
		if err != nil {
			log.Fatalf("server start: %v", err)
		}
		return
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGraceDuration)
	defer cancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		// Already shutting down — log and continue. Fatalf would emit
		// non-zero exit and trigger restart loops in supervisors that
		// treat clean-but-slow shutdown as a crash.
		log.Printf("graceful shutdown: %v", err)
	}
}
