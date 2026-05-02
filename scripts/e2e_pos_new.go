//go:build ignore

// e2e_pos_new walks the /pos/new feature end-to-end against the real
// handler tree + real Postgres. Equivalent to a browser-driven test
// for a no-JS form: same HTTP requests a real browser would make.
//
//	export DATABASE_URL=postgres://postgres@localhost:5432/financial_shima?sslmode=disable
//	go run ./scripts/e2e_pos_new.go
//
// Prints a PASS/FAIL line per step. Exits non-zero on first failure.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/dependencies/assistant"
	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/clock"
	"github.com/rizaramadan/financial-shima/logic/idgen"
	"github.com/rizaramadan/financial-shima/logic/user"
	"github.com/rizaramadan/financial-shima/web/handler"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		die("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		die("pool: %v", err)
	}
	defer pool.Close()

	rec := &assistant.Recorder{}
	a := auth.New(user.Seeded(), clock.System{}, rand.Reader, idgen.Crypto{})
	h := handler.New(a, rec, pool)

	e := echo.New()
	e.Renderer = template.New()
	e.Use(mw.Session(a))
	e.GET("/", h.HomeGet)
	e.GET("/login", h.LoginGet)
	e.POST("/login", h.LoginPost)
	e.GET("/verify", h.VerifyGet)
	e.POST("/verify", h.VerifyPost)
	e.POST("/logout", h.LogoutPost)
	e.GET("/pos/new", h.PosNewGet)
	e.POST("/pos", h.PosNewPost)
	e.GET("/pos/:id", h.PosGet)

	srv := httptest.NewServer(e)
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		// Don't auto-follow redirects so we can assert each step.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 5 * time.Second,
	}

	pass("server up at " + srv.URL)

	// 1. GET /pos/new while unauthenticated → 303 to /login.
	r1, err := client.Get(srv.URL + "/pos/new")
	if err != nil {
		die("GET /pos/new (unauth): %v", err)
	}
	if r1.StatusCode != http.StatusSeeOther || r1.Header.Get("Location") != "/login" {
		die("unauth /pos/new: status=%d Location=%q (want 303 → /login)", r1.StatusCode, r1.Header.Get("Location"))
	}
	pass("S1 unauth /pos/new redirects to /login")

	// 2. POST /login {identifier=@riza_ramadan} → 303 → /verify?id=…
	form := url.Values{"identifier": {"@riza_ramadan"}}
	r2, err := client.PostForm(srv.URL+"/login", form)
	if err != nil {
		die("POST /login: %v", err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusSeeOther {
		die("POST /login: status=%d (want 303)", r2.StatusCode)
	}
	if !strings.HasPrefix(r2.Header.Get("Location"), "/verify?id=") {
		die("POST /login: Location=%q (want /verify?id=…)", r2.Header.Get("Location"))
	}
	pass("S2 POST /login → 303 → " + r2.Header.Get("Location"))

	// 3. Read the OTP straight off the in-process Recorder (the
	// assistant Bot would normally Telegram it; here we eavesdrop).
	last, ok := rec.Last()
	if !ok {
		die("no OTP recorded — did user lookup fail?")
	}
	pass("S3 OTP issued: " + redact(last.Code))

	// 4. POST /verify {identifier, code} → 303 to /.
	form = url.Values{"identifier": {"@riza_ramadan"}, "code": {last.Code}}
	r4, err := client.PostForm(srv.URL+"/verify", form)
	if err != nil {
		die("POST /verify: %v", err)
	}
	r4.Body.Close()
	if r4.StatusCode != http.StatusSeeOther {
		die("POST /verify: status=%d (want 303)", r4.StatusCode)
	}
	pass("S4 POST /verify → 303 (signed in)")

	// 5. GET /pos/new with cookie → 200 + form.
	r5, err := client.Get(srv.URL + "/pos/new")
	if err != nil {
		die("GET /pos/new: %v", err)
	}
	body5, _ := io.ReadAll(r5.Body)
	r5.Body.Close()
	if r5.StatusCode != http.StatusOK {
		die("GET /pos/new: status=%d", r5.StatusCode)
	}
	for _, want := range []string{"<h1>New Pos</h1>", `name="name"`, `name="currency"`, `action="/pos"`} {
		if !strings.Contains(string(body5), want) {
			die("GET /pos/new body missing %q", want)
		}
	}
	pass("S5 GET /pos/new authenticated → 200 + complete form")

	// 6. POST /pos with INVALID currency → 200 (re-render with error,
	// not 500). User input round-trips. AND no row leaks into the DB.
	invalidName := fmt.Sprintf("invalid-%d", time.Now().UnixNano())
	form = url.Values{"name": {invalidName}, "currency": {"BAD CURRENCY"}}
	r6, err := client.PostForm(srv.URL+"/pos", form)
	if err != nil {
		die("POST /pos (invalid): %v", err)
	}
	body6, _ := io.ReadAll(r6.Body)
	r6.Body.Close()
	if r6.StatusCode != http.StatusOK {
		die("POST /pos (invalid currency): status=%d (want 200 with errors)", r6.StatusCode)
	}
	if !strings.Contains(string(body6), "lowercase") {
		die("POST /pos (invalid): body missing lowercase-error")
	}
	if !strings.Contains(string(body6), `value="`+invalidName+`"`) {
		die("POST /pos (invalid): name not echoed back into form")
	}
	// DB invariant: validation failure means NO row inserted.
	var rogueCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pos WHERE name = $1`, invalidName,
	).Scan(&rogueCount); err != nil {
		die("DB count after invalid POST: %v", err)
	}
	if rogueCount != 0 {
		die("invalid POST leaked %d row(s) into DB (want 0)", rogueCount)
	}
	pass("S6 POST /pos invalid currency → 200 + error + DB row count = 0")

	// 7. POST /pos with VALID input → 303 to /pos/<uuid>.
	uniqueName := fmt.Sprintf("e2e-%d", time.Now().UnixNano())
	form = url.Values{
		"name":     {uniqueName},
		"currency": {"idr"},
		"target":   {"7777777"},
	}
	r7, err := client.PostForm(srv.URL+"/pos", form)
	if err != nil {
		die("POST /pos (valid): %v", err)
	}
	r7.Body.Close()
	if r7.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(r7.Body)
		die("POST /pos (valid): status=%d body=%s", r7.StatusCode, string(body))
	}
	loc := r7.Header.Get("Location")
	if !strings.HasPrefix(loc, "/pos/") || len(loc) < len("/pos/")+30 {
		die("POST /pos (valid): bad Location=%q", loc)
	}
	pass("S7 POST /pos valid → 303 → " + loc)

	// 8. Follow redirect → assert new Pos appears with formatted target.
	r8, err := client.Get(srv.URL + loc)
	if err != nil {
		die("GET %s: %v", loc, err)
	}
	body8, _ := io.ReadAll(r8.Body)
	r8.Body.Close()
	if r8.StatusCode != http.StatusOK {
		die("GET %s: status=%d", loc, r8.StatusCode)
	}
	for _, want := range []string{
		"<h1>" + uniqueName + "</h1>",
		"target Rp 7.777.777", // money formatter rendered correctly
	} {
		if !strings.Contains(string(body8), want) {
			die("Pos detail missing %q in body", want)
		}
	}
	pass("S8 GET " + loc + " → 200 + formatted target Rp 7.777.777")

	// 9. POST same name+currency again → form re-renders with dedup error
	// (UNIQUE (name, currency) caught and surfaced as form error). AND
	// the DB row count for this name does NOT go up (UNIQUE actually
	// holds at the DB layer, not just the app layer).
	r9, err := client.PostForm(srv.URL+"/pos", form)
	if err != nil {
		die("POST /pos (dup): %v", err)
	}
	body9, _ := io.ReadAll(r9.Body)
	r9.Body.Close()
	if r9.StatusCode != http.StatusOK {
		die("POST /pos (dup): status=%d (want 200)", r9.StatusCode)
	}
	if !strings.Contains(string(body9), "already exists") {
		die("POST /pos (dup): body missing dedup error")
	}
	var dupCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pos WHERE name = $1 AND currency = 'idr'`, uniqueName,
	).Scan(&dupCount); err != nil {
		die("DB count after dup POST: %v", err)
	}
	if dupCount != 1 {
		die("duplicate POST changed row count to %d (want 1)", dupCount)
	}
	pass("S9 POST /pos duplicate → 200 + error + DB row count stays 1")

	// 10. Full row consistency: every column is what spec §4.2 says.
	var (
		dbName, dbCurrency string
		dbTarget           *int64
		dbArchived         bool
		dbCreatedAt        time.Time
	)
	if err := pool.QueryRow(ctx, `
		SELECT name, currency, target, archived, created_at
		  FROM pos WHERE name = $1 AND currency = 'idr'`,
		uniqueName,
	).Scan(&dbName, &dbCurrency, &dbTarget, &dbArchived, &dbCreatedAt); err != nil {
		die("DB row not found: %v", err)
	}
	if dbName != uniqueName {
		die("DB name=%q (want %q)", dbName, uniqueName)
	}
	if dbCurrency != "idr" {
		die("DB currency=%q (want idr)", dbCurrency)
	}
	if dbTarget == nil || *dbTarget != 7777777 {
		die("DB target=%v (want 7777777)", dbTarget)
	}
	if dbArchived {
		die("DB archived=true on freshly-created Pos (want false)")
	}
	if time.Since(dbCreatedAt) > 30*time.Second {
		die("DB created_at=%v is older than 30s — schema default not applied?", dbCreatedAt)
	}
	pass("S10 DB row consistency: name+currency+target match, archived=false, created_at fresh")

	// 11. Whitespace normalization round-trip: form value with padding
	// must land trimmed in the DB (logic/pos.Normalize). And the
	// currency must be lowercased even if user typed uppercase.
	paddedName := fmt.Sprintf("  norm-%d  ", time.Now().UnixNano())
	expectedTrimmed := strings.TrimSpace(paddedName)
	form = url.Values{
		"name":     {paddedName},
		"currency": {"  IDR  "}, // mixed case + padding
	}
	r11, err := client.PostForm(srv.URL+"/pos", form)
	if err != nil {
		die("POST /pos (normalize): %v", err)
	}
	r11.Body.Close()
	if r11.StatusCode != http.StatusSeeOther {
		die("POST /pos (normalize): status=%d (want 303)", r11.StatusCode)
	}
	var normName, normCurrency string
	if err := pool.QueryRow(ctx,
		`SELECT name, currency FROM pos WHERE name = $1`, expectedTrimmed,
	).Scan(&normName, &normCurrency); err != nil {
		die("normalized row not found by trimmed name %q: %v", expectedTrimmed, err)
	}
	if normName != expectedTrimmed {
		die("DB name=%q (want trimmed %q)", normName, expectedTrimmed)
	}
	if normCurrency != "idr" {
		die("DB currency=%q (want lowercased 'idr')", normCurrency)
	}
	pass("S11 normalization: padded \"  …  \" trimmed; uppercase IDR lowercased to 'idr'")

	// Cleanup: delete the test rows so subsequent runs don't bloat
	// the seed.
	if _, err := pool.Exec(ctx,
		`DELETE FROM pos WHERE name = ANY($1)`,
		[]string{uniqueName, expectedTrimmed},
	); err != nil {
		fmt.Printf("WARN cleanup: %v\n", err)
	}

	fmt.Println()
	fmt.Println("PASS — /pos/new feature works end-to-end (validity + consistency)")
}

func pass(msg string) { fmt.Printf("✓ %s\n", msg) }
func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "✗ "+format+"\n", args...)
	os.Exit(1)
}
func redact(s string) string {
	return strings.Repeat("•", len(s)) + " (" + fmt.Sprintf("%d digits", len(s)) + ")"
}

// pin import that's only used in helpers
var _ = bytes.Buffer{}
