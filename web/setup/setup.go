// Package setup applies the project's standard Echo configuration —
// timeouts plus middleware — to a fresh *echo.Echo. Both the production
// bootstrap (cmd/server) and tests that want to exercise the assembled HTTP
// stack call Apply, so handler tests see the same middleware chain that
// production traffic sees.
package setup

import (
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

const (
	ReadHeaderTimeout = 5 * time.Second
	ReadTimeout       = 10 * time.Second

	// WriteTimeout caps total request duration (read body + handler + write).
	// Phase 1 serves static HTML in microseconds, so 10s is generous.
	// TODO(phase 2): when the POST handler calls Telegram's sendMessage API
	// (synchronous, with its own 5s timeout per spec §7.3), evaluate whether
	// to bump this to ~30s or push slow work behind per-handler context
	// deadlines instead.
	WriteTimeout = 10 * time.Second

	IdleTimeout = 60 * time.Second
)

// Apply configures e with the project's standard timeouts and middleware.
// Routes are registered by the caller after Apply returns.
func Apply(e *echo.Echo) {
	e.HideBanner = true

	e.Server.ReadHeaderTimeout = ReadHeaderTimeout
	e.Server.ReadTimeout = ReadTimeout
	e.Server.WriteTimeout = WriteTimeout
	e.Server.IdleTimeout = IdleTimeout

	e.Use(middleware.Recover())
	e.Use(middleware.SecureWithConfig(middleware.SecureConfig{
		XSSProtection:         "0", // Modern browsers ignore this; CSP is the defence.
		ContentTypeNosniff:    "nosniff",
		XFrameOptions:         "DENY",
		ReferrerPolicy:        "no-referrer",
		ContentSecurityPolicy: "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'",
	}))
}
