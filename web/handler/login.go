package handler

import (
	"crypto/subtle"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/dependencies/assistant"
	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/user"
	"github.com/rizaramadan/financial-shima/web/template"
)

const SessionCookieName = "shima_session"

// Handlers wires the Logic and Dependency layers into Echo handler functions.
// Constructed once at boot and registered against the routes in cmd/server.
//
// DB is optional: nil means "no DB wired" — handlers that need data fall
// back to a placeholder render, which keeps cmd/server tests runnable
// without a Postgres.
//
// LoginPassword is the shared password compared against the form value at
// /login. Empty means login is not configured and every attempt is rejected.
type Handlers struct {
	Auth          *auth.Auth
	Assistant     assistant.Client
	DB            *pgxpool.Pool
	LoginPassword string
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

// LoginPost looks up the user, checks the form password against
// h.LoginPassword (LOGIN_PASSWORD env var), and on match mints a session
// cookie. The OTP path is unreachable from this handler.
func (h *Handlers) LoginPost(c echo.Context) error {
	identifier := c.FormValue("identifier")
	password := c.FormValue("password")

	u, ok := user.Find(identifier, h.Auth.Users)
	if !ok {
		return c.Render(http.StatusOK, "login", template.LoginData{
			Title: "Sign in",
			Error: "User not found.",
		})
	}

	// Reject when the env var is unset so a misconfigured deploy can't be
	// signed into with an empty password.
	if h.LoginPassword == "" ||
		subtle.ConstantTimeCompare([]byte(password), []byte(h.LoginPassword)) != 1 {
		return c.Render(http.StatusOK, "login", template.LoginData{
			Title: "Sign in",
			Error: "Invalid password.",
		})
	}

	s := h.Auth.MintSession(u)
	c.SetCookie(&http.Cookie{
		Name:     SessionCookieName,
		Value:    s.Token,
		Path:     "/",
		Expires:  s.ExpiresAt,
		HttpOnly: true,
		Secure:   c.Request().TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
	return c.Redirect(http.StatusSeeOther, "/")
}
