package main

import (
	"context"
	"errors"
	"log"
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
	defaultAddr           = ":8080"
	readHeaderTimeout     = 5 * time.Second
	readTimeout           = 10 * time.Second
	writeTimeout          = 10 * time.Second
	idleTimeout           = 60 * time.Second
	shutdownGraceDuration = 10 * time.Second
)

// newServer wires routes and middleware. Server timeouts are set on the
// embedded *http.Server so the process is safe to expose without an explicit
// front-end. Tests construct via newServer() and call ServeHTTP directly.
func newServer() *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

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

	e := newServer()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("listening on %s", addr)
		if err := e.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Print("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGraceDuration)
	defer cancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("graceful shutdown: %v", err)
	}
}
