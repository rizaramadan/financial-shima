package handler

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/otp"
	"github.com/rizaramadan/financial-shima/web/template"
)

// VerifyGet renders the OTP-entry form. Identifier comes from the ?id query
// string set by the LoginPost redirect; missing/blank id sends the user back
// to /login (no OTP could have been issued without it).
func (h *Handlers) VerifyGet(c echo.Context) error {
	id := strings.TrimSpace(c.QueryParam("id"))
	if id == "" {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	return c.Render(http.StatusOK, "verify", template.VerifyData{
		Title:      "Enter code",
		Identifier: id,
	})
}

// VerifyPost runs spec §3.2 step 6 + sets the session cookie on Accepted.
func (h *Handlers) VerifyPost(c echo.Context) error {
	identifier := strings.TrimSpace(c.FormValue("identifier"))
	codeStr := strings.TrimSpace(c.FormValue("code"))

	render := func(status int, errMsg string) error {
		return c.Render(status, "verify", template.VerifyData{
			Title:      "Enter code",
			Identifier: identifier,
			Error:      errMsg,
		})
	}

	if identifier == "" {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	if len(codeStr) != 6 {
		return render(http.StatusOK, "Code must be 6 digits.")
	}
	n := 0
	for _, r := range codeStr {
		if r < '0' || r > '9' {
			return render(http.StatusOK, "Code must be 6 digits.")
		}
		n = n*10 + int(r-'0')
	}

	out := h.Auth.Verify(identifier, otp.NewCode(n))
	switch out.Result {
	case auth.Verified:
		c.SetCookie(&http.Cookie{
			Name:     SessionCookieName,
			Value:    out.Session.Token,
			Path:     "/",
			Expires:  out.Session.ExpiresAt,
			HttpOnly: true,
			Secure:   c.Request().TLS != nil,
			SameSite: http.SameSiteLaxMode,
		})
		return c.Redirect(http.StatusSeeOther, "/")
	case auth.Locked:
		return render(http.StatusOK, "Too many attempts. Request a new code.")
	case auth.Expired:
		return render(http.StatusOK, "Code expired. Request a new one.")
	case auth.Rejected:
		return render(http.StatusOK, "That code did not match. Try again.")
	case auth.NoOTP:
		// No record on file — the previous Issue likely never landed, or
		// the user is replaying an old form. Redirect to start.
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	return render(http.StatusInternalServerError, "Something went wrong.")
}
