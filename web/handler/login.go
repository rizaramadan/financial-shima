package handler

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/dependencies/assistant"
	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/web/template"
)

const SessionCookieName = "shima_session"

// Handlers wires the Logic and Dependency layers into Echo handler functions.
// Constructed once at boot and registered against the routes in cmd/server.
//
// DB is optional: nil means "no DB wired" — handlers that need data fall
// back to a placeholder render, which keeps cmd/server tests runnable
// without a Postgres.
type Handlers struct {
	Auth      *auth.Auth
	Assistant assistant.Client
	DB        *pgxpool.Pool
}

func New(a *auth.Auth, ac assistant.Client, db *pgxpool.Pool) *Handlers {
	if a == nil {
		panic("handler.New: nil Auth")
	}
	if ac == nil {
		panic("handler.New: nil Assistant")
	}
	return &Handlers{Auth: a, Assistant: ac, DB: db}
}

// LoginGet renders the sign-in form.
func (h *Handlers) LoginGet(c echo.Context) error {
	return c.Render(http.StatusOK, "login", template.LoginData{Title: "Sign in"})
}

// LoginPost runs spec §3.2 steps 2-4: lookup user, generate OTP, hand off to
// the assistant, then redirect to /verify. Errors are surfaced inline on the
// re-rendered login page.
func (h *Handlers) LoginPost(c echo.Context) error {
	identifier := c.FormValue("identifier")
	out := h.Auth.Issue(identifier)

	switch out.Result {
	case auth.UserNotFound:
		return c.Render(http.StatusOK, "login", template.LoginData{
			Title: "Sign in",
			Error: "User not found.",
		})
	case auth.CooldownActive:
		return c.Render(http.StatusOK, "login", template.LoginData{
			Title: "Sign in",
			Error: "A code was just sent. Please wait a moment and try again.",
		})
	case auth.Issued:
		ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
		defer cancel()
		if err := h.Assistant.SendOTP(ctx, out.Code.String(), out.User.DisplayName); err != nil {
			log.Printf("assistant SendOTP: %v", err)
			return c.Render(http.StatusOK, "login", template.LoginData{
				Title: "Sign in",
				Error: "Failed to send OTP. Try again.",
			})
		}
		// Identifier in query string, NOT in a cookie — there's nothing
		// sensitive here (the user just typed it) and a cookie would be
		// visible to other tabs. URL-escape so '#' / '&' / '?' don't
		// smuggle in extra query syntax (Skeet R6 review).
		return c.Redirect(http.StatusSeeOther, "/verify?id="+url.QueryEscape(identifier))
	}

	// auth.Issue covers all three cases; fall-through is a bug.
	return c.Render(http.StatusInternalServerError, "login", template.LoginData{
		Title: "Sign in",
		Error: "Something went wrong. Please try again.",
	})
}
