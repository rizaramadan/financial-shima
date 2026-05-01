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
	"github.com/labstack/echo/v4/middleware"

	"github.com/rizaramadan/financial-shima/web/handler"
)

const (
	// defaultAddr is the listen address when ADDR is unset. ADDR accepts any
	// value valid for net.Listen("tcp", ...): ":8080", "127.0.0.1:8080",
	// "[::1]:8080".
	defaultAddr = ":8080"

	// readHeaderTimeout / readTimeout / writeTimeout / idleTimeout cap the
	// time a request can spend at each phase. Defaults Slowloris-safe.
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second

	// writeTimeout caps total request duration (read body + handler + write).
	// Phase 1 serves static HTML in microseconds, so 10s is generous.
	// TODO(phase 2): when the POST handler calls Telegram's sendMessage API
	// (synchronous, with its own 5s timeout per spec §7.3), evaluate whether
	// to bump this to ~30s or push slow work behind per-handler context
	// deadlines instead.
	writeTimeout = 10 * time.Second

	idleTimeout           = 60 * time.Second
	shutdownGraceDuration = 10 * time.Second
)

// newServer wires routes and middleware. Server timeouts are set on the
// embedded *http.Server so the process is safe to expose without an explicit
// reverse proxy. Tests construct via newServer() and call ServeHTTP directly.
func newServer() *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	// Echo prints "⇨ http server started on …" on successful bind; that is
	// the single source of truth for "it's actually listening." main() does
	// not duplicate this with its own pre-bind log.

	e.Server.ReadHeaderTimeout = readHeaderTimeout
	e.Server.ReadTimeout = readTimeout
	e.Server.WriteTimeout = writeTimeout
	e.Server.IdleTimeout = idleTimeout

	e.Use(middleware.Recover())
	e.Use(middleware.SecureWithConfig(middleware.SecureConfig{
		XSSProtection:         "0", // Modern browsers ignore this; CSP is the defence.
		ContentTypeNosniff:    "nosniff",
		XFrameOptions:         "DENY",
		ReferrerPolicy:        "no-referrer",
		ContentSecurityPolicy: "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'",
	}))

	e.GET("/login", handler.LoginGet)

	return e
}

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = defaultAddr
	}
	if _, err := net.ResolveTCPAddr("tcp", addr); err != nil {
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
		err := e.Start(addr)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case <-ctx.Done():
		log.Print("shutdown signal received")
	case err := <-serverErr:
		if err != nil {
			log.Printf("server start: %v", err)
		}
		// On a bind failure there's nothing to drain; skip Shutdown.
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
