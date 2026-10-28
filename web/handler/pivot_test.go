package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/dependencies/assistant"
	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/clock"
	"github.com/rizaramadan/financial-shima/logic/idgen"
	"github.com/rizaramadan/financial-shima/logic/user"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	tplpkg "github.com/rizaramadan/financial-shima/web/template"
)

func pivotTestServer(t *testing.T, signedIn user.User, signedInOK bool) *echo.Echo {
	t.Helper()
	src := bytes.NewReader(make([]byte, 64))
	a := auth.New(user.Seeded(), clock.Fixed{T: t0}, src, idgen.Fixed{Value: "tok"})
	h := New(a, &assistant.Recorder{}, nil)

	e := echo.New()
	e.Renderer = tplpkg.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if signedInOK {
				c.Set(mw.SessionContextKey, signedIn)
			}
			return next(c)
		}
	})
	e.GET("/pivot", h.PivotGet)
	return e
}

func TestPivotGet_Unauthenticated_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	e := pivotTestServer(t, user.User{}, false)
	req := httptest.NewRequest(http.MethodGet, "/pivot", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestPivotGet_NilPool_RendersFiltersAndEmptyStateScaffolding(t *testing.T) {
	t.Parallel()
	signedIn := user.User{ID: "u-1", DisplayName: "Tester"}
	e := pivotTestServer(t, signedIn, true)
	req := httptest.NewRequest(http.MethodGet, "/pivot", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<h1>Pivot Roll-forward</h1>`,
		`id="pivot-akun"`,
		`id="pivot-body"`,
		`id="pivot-foot"`,
		`id="pivot-data"`,
		`id="pivot-filters" hidden`,
		`id="pivot-toggle"`,
		`data-fgroup`,
		`data-fchecks`,
		`id="pivot-nokat"`,
		`Show filters`,
		`No movements match the current filters`,
		`Reset filters`,
		`>Akun</th><th>Kategori</th><th>Nama</th>`,
		`This month`,
		`This quarter`,
		`This year`,
		`All time`,
		`Money in`,
		`Money out`,
		`Show empty positions`,
		`Show Akun totals`,
		`Expand all`,
		`Collapse all`,
		`Search Nama`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pivot page missing %q", want)
		}
	}
	if !strings.Contains(body, `href="/notifications"`) {
		t.Error("nav missing Notifications link on pivot page")
	}
	// Order is toggle, filter panels, then the report table.
	if !(strings.Index(body, `id="pivot-toggle"`) < strings.Index(body, `id="pivot-filters"`) &&
		strings.Index(body, `id="pivot-filters"`) < strings.Index(body, `id="pivot-body"`)) {
		t.Error("order should be filter toggle, filter panels, then report table")
	}
	// Empty dataset encodes as empty arrays so the client boots clean.
	if !strings.Contains(body, `{"accounts":[],"movements":[],"positions":[]}`) &&
		!strings.Contains(body, `"movements":[]`) {
		t.Error("pivot page missing empty movements dataset")
	}
}

// TestPivotTemplate_PopulatedData renders the template with a dataset
// where the same Kategori name lives under two Akun, pinning the
// identity rule in markup: two separate checklist entries, each visibly
// tied to its Akun, plus the signed movements in the JSON payload.
func TestPivotTemplate_PopulatedData(t *testing.T) {
	t.Parallel()
	r := tplpkg.New()
	data := tplpkg.PivotData{Title: "Pivot", DisplayName: "Tester"}
	data.Accounts = []tplpkg.PivotAccount{
		{ID: "a-bsi", Name: "BSI Shima"},
		{ID: "a-dana", Name: "Dana"},
	}
	data.Positions = []tplpkg.PivotPosition{
		{PosID: "p-sampah-bsi", Name: "Sampah", AccountID: "a-bsi", AccountName: "BSI Shima"},
		{PosID: "p-sampah-dana", Name: "Sampah", AccountID: "a-dana", AccountName: "Dana"},
	}
	for _, p := range data.Positions {
		g := data.GroupFor(p.AccountID)
		g.AccountName = p.AccountName
		g.Positions = append(g.Positions, p)
	}
	data.Movements = []tplpkg.PivotMovement{
		{ID: "m1", PosID: "p-sampah-bsi", AccountID: "a-bsi", AccountName: "BSI Shima", PosName: "Sampah", Nama: "Sept 26", Date: "2026-09-01", Amount: 500000, Type: "money_in"},
		{ID: "m2", PosID: "p-sampah-dana", AccountID: "a-dana", AccountName: "Dana", PosName: "Sampah", Nama: "daftar SAB (23-10)", Date: "2026-10-23", Amount: -7350000, Type: "money_out"},
	}
	var buf bytes.Buffer
	if err := r.Render(&buf, "pivot", data, nil); err != nil {
		t.Fatalf("render pivot: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`Sampah <span class="pivot-akun-tag">&middot; BSI Shima</span>`,
		`Sampah <span class="pivot-akun-tag">&middot; Dana</span>`,
		`data-kat="p-sampah-bsi"`,
		`data-kat="p-sampah-dana"`,
		`data-group="a-bsi"`,
		`data-fgroup="a-bsi"`,
		`data-fchecks="a-bsi"`,
		`"Nama":"Sept 26"`,
		`"Amount":500000`,
		`"Amount":-7350000`,
		`<a href="/pivot"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pivot page missing %q", want)
		}
	}
}
