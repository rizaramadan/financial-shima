package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/web/handler"
	"github.com/rizaramadan/financial-shima/web/setup"
)

const (
	// defaultAddr is the listen address when ADDR is unset. ADDR accepts any
	// value valid for net.Listen("tcp", ...): ":8080", "127.0.0.1:8080",
	// "[::1]:8080".
	defaultAddr = ":8080"
)

// shutdownGraceDuration must be at least setup.WriteTimeout so in-flight
// requests granted the full write budget can complete before forced close.
// Bumping setup.WriteTimeout in Phase 2 (for Telegram calls) bumps this too.
var shutdownGraceDuration = setup.WriteTimeout

func newServer() *echo.Echo {
	e := echo.New()
	setup.Apply(e)
	e.GET("/login", handler.LoginGet)
	e.POST("/login", handler.LoginPost) // Phase 1 stub: 501 until Phase 2 wires OTP.
	return e
}

// validateAddr performs syntactic validation only — no network I/O. It
// rejects empty host:port pairs, non-numeric ports, and ports outside the
// 1-65535 range. (Port 0 is valid for net.Listen — "OS-chosen port" — but
// almost always operator error in production, so we reject it here.)
func validateAddr(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return errors.New("port must be numeric, got " + strconv.Quote(port))
	}
	if p < 1 || p > 65535 {
		return errors.New("port must be 1-65535, got " + strconv.Itoa(p))
	}
	return nil
}

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = defaultAddr
	}
	if err := validateAddr(addr); err != nil {
		log.Fatalf("invalid ADDR %q: %v", addr, err)
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
		// Log immediately before bind. There's a microsecond window between
		// this and the actual bind, but no log at all is strictly worse —
		// silent startup is indistinguishable from "hung pre-listen" in
		// production logs.
		log.Printf("listening on %s", addr)
		if err := e.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Print("shutdown signal received")
		// Drain in case Start() failed concurrently with the signal —
		// otherwise that error is swallowed and we'd Shutdown a server
		// that never bound. The buffered channel + non-blocking read make
		// this safe.
		select {
		case err := <-serverErr:
			if err != nil {
				log.Fatalf("server start (during shutdown): %v", err)
			}
		default:
		}
	case err := <-serverErr:
		// Bind failed (port in use, permission denied, etc.). Exit non-zero
		// so supervisors (systemd, k8s) treat it as a crash and restart.
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
