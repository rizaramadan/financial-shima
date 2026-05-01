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
	"strconv"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/web/handler"
	"github.com/rizaramadan/financial-shima/web/setup"
)

const defaultAddr = ":8080"

// shutdownGraceDuration is at least setup.WriteTimeout so an in-flight request
// granted the full write budget can complete; the +1s slack covers the gap
// between "write deadline expires" and "handler returns and Shutdown can
// complete its accounting" — racy on slow machines without it.
var shutdownGraceDuration = setup.WriteTimeout + 1*time.Second

func newServer() *echo.Echo {
	e := echo.New()
	setup.Apply(e)
	e.GET("/login", handler.LoginGet)
	e.POST("/login", handler.LoginPost) // Phase 1 stub: 501 until Phase 2 wires OTP.
	return e
}

// validateAddr performs syntactic validation only — no network I/O. It rejects
// non-numeric ports and ports outside the 1-65535 range. Port 0 (OS-chosen)
// is rejected as almost-always operator error in production.
func validateAddr(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("port %q is not numeric: %w", port, err)
	}
	if p < 1 || p > 65535 {
		return fmt.Errorf("port %d outside 1-65535", p)
	}
	return nil
}

// isBenignServerErr reports whether err from echo.Start is the expected
// signal of clean shutdown rather than a real failure.
func isBenignServerErr(err error) bool {
	return errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed)
}

// run owns the server lifecycle. It returns when ctx is cancelled or when
// e.Start returns a non-benign error. main()'s only job after wiring signals
// is to call run and exit on its return.
//
// run is exported package-internally so a test can drive it with a cancellable
// context and assert clean shutdown without spawning a process.
func run(ctx context.Context, e *echo.Echo, addr string) error {
	serverErr := make(chan error, 1)
	go func() {
		defer close(serverErr)
		log.Printf("listening on %s", addr)
		if err := e.Start(addr); err != nil && !isBenignServerErr(err) {
			serverErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Print("shutdown signal received")
		// Drain in case Start() failed concurrently with the signal —
		// otherwise that error is swallowed and we'd Shutdown a server
		// that never bound. Buffered chan + non-blocking read is safe.
		select {
		case err := <-serverErr:
			if err != nil {
				return fmt.Errorf("server start during shutdown: %w", err)
			}
		default:
		}
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("server start: %w", err)
		}
		return nil // server stopped cleanly without a signal
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGraceDuration)
	defer cancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		// Already shutting down — log and continue. Returning an error
		// here would propagate to log.Fatal and trigger restart loops in
		// supervisors that treat clean-but-slow shutdown as a crash.
		log.Printf("graceful shutdown: %v", err)
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

	// SIGTERM is the signal sent by systemd / Kubernetes for graceful
	// shutdown. On Windows, only os.Interrupt (Ctrl+C) is delivered;
	// SIGTERM is a no-op there. Production runs on Linux.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, newServer(), addr); err != nil {
		log.Fatal(err)
	}
}
