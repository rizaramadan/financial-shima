package template

// Regression tests for the October 2026 live UI audit fixes. These pin
// the markup/CSS contracts (theme-aware ink, backdrop scoping, drawer
// wiring, filter order) without needing a browser or database.

import (
	"bytes"
	"strings"
	"testing"
)

func renderPage(t *testing.T, name string, data interface{}) string {
	t.Helper()
	r := New()
	var buf bytes.Buffer
	if err := r.Render(&buf, name, data, nil); err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	return buf.String()
}

func signedPivot() PivotData {
	return PivotData{Title: "Pivot", DisplayName: "Tester", UnreadCount: 3}
}

// TestLayout_SuccessTokenMeetsContrast: the light success token is
// green-700 (#15803D, 4.81:1 on #FAFAFA), not green-600 (#16A34A,
// 3.16:1). Both light blocks (default :root and explicit data-theme)
// carry it; dark keeps green-400.
func TestLayout_SuccessTokenMeetsContrast(t *testing.T) {
	t.Parallel()
	body := renderPage(t, "pivot", signedPivot())
	if strings.Contains(body, "#16A34A") {
		t.Error("layout still contains #16A34A (fails 4.5:1 on light surfaces)")
	}
	if n := strings.Count(body, "--success: #15803D;"); n != 2 {
		t.Errorf("--success: #15803D appears %d times, want 2 (default + explicit light)", n)
	}
	if !strings.Contains(body, "--success: #4ADE80;") {
		t.Error("dark --success token missing")
	}
}

// TestLayout_PivotSubtotalInk: pivot subtotal rows must use theme-aware
// table ink, neutralizing Pico's [role=button] primary-button paint
// (white / green-900 at ~1:1 contrast).
func TestLayout_PivotSubtotalInk(t *testing.T) {
	t.Parallel()
	body := renderPage(t, "pivot", signedPivot())
	for _, want := range []string{
		`tr.pivot-sub[role="button"]`,
		`color: var(--text); text-align: left;`,
		`.pivot-sub td.num { text-align: right; }`,
		`.pivot-sub td.pivot-akun-cell { color: var(--text-secondary); }`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("layout missing %q", want)
		}
	}
}

// TestLayout_SidebarOverlayScoped: the backdrop must lose to nothing —
// selectors repeat the element so Pico's [type=checkbox]~label rule
// cannot keep it painted, and desktop never shows it.
func TestLayout_SidebarOverlayScoped(t *testing.T) {
	t.Parallel()
	body := renderPage(t, "pivot", signedPivot())
	for _, want := range []string{
		`label.sidebar-overlay {`,
		`.sidebar-toggle:checked ~ label.sidebar-overlay {`,
		`visibility: hidden;`,
		`pointer-events: none;`,
		`pointer-events: auto;`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("layout missing %q", want)
		}
	}
}

// TestLayout_HamburgerTarget: 44px hit area without Pico's green
// button paint.
func TestLayout_HamburgerTarget(t *testing.T) {
	t.Parallel()
	body := renderPage(t, "pivot", signedPivot())
	for _, want := range []string{
		`label.hamburger {`,
		`width: 44px; height: 44px; padding: 0;`,
		`background-color: transparent;`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("layout missing %q", want)
		}
	}
}

// TestLayout_TouchRowTargets: expandable pivot rows reach 44px on
// touch layouts.
func TestLayout_TouchRowTargets(t *testing.T) {
	t.Parallel()
	body := renderPage(t, "pivot", signedPivot())
	if !strings.Contains(body, `.pivot-wrap tr.pivot-sub td { padding-top: 13px; padding-bottom: 13px; }`) {
		t.Error("layout missing touch-layout pivot row hit-area rule")
	}
}

// TestLayout_DrawerWiring: the checkbox toggle must not be aria-hidden
// (label clicks focus it — the audit's console warning), the hamburger
// exposes expanded/controls, and the focus-management script is present.
func TestLayout_DrawerWiring(t *testing.T) {
	t.Parallel()
	body := renderPage(t, "pivot", signedPivot())
	for _, want := range []string{
		`id="sidebar-toggle" class="sidebar-toggle" tabindex="-1"`,
		`<label for="sidebar-toggle" class="sidebar-overlay"></label>`,
		`aria-expanded="false" aria-controls="sidebar"`,
		`<aside class="sidebar" id="sidebar"`,
		`main.inert = true`,
		`main.inert = false`,
		`=== 'Escape'`,
		`toggle.dispatchEvent(new Event('change'))`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("layout missing %q", want)
		}
	}
	if strings.Contains(body, `id="sidebar-toggle" class="sidebar-toggle" aria-hidden`) {
		t.Error("sidebar toggle must not carry aria-hidden")
	}
}

// TestPivot_FilterOrder: common controls (Period/Movement/View) render
// before the long Akun/Kategori lists so the report stays near them.
func TestPivot_FilterOrder(t *testing.T) {
	t.Parallel()
	body := renderPage(t, "pivot", signedPivot())
	filters := strings.Index(body, `>Filters</span>`)
	akun := strings.Index(body, `>Akun</span>`)
	kat := strings.Index(body, `>Kategori</span>`)
	report := strings.Index(body, `>Report</span>`)
	if filters < 0 || akun < 0 || kat < 0 || report < 0 {
		t.Fatal("pivot page missing filter card titles")
	}
	if !(filters < akun && akun < kat && kat < report) {
		t.Error("pivot filter order should be Filters, Akun, Kategori, Report")
	}
}

// TestPivot_ChecklistSearch: bounded, searchable checklists that never
// touch the URL.
func TestPivot_ChecklistSearch(t *testing.T) {
	t.Parallel()
	body := renderPage(t, "pivot", signedPivot())
	for _, want := range []string{
		`id="pivot-akun-q"`,
		`id="pivot-kat-q"`,
		`pivot-checks pivot-scroll`,
		`id="pivot-nokat-text"`,
		`.pivot-scroll { max-height: 240px; overflow-y: auto; overscroll-behavior: contain; }`,
		`var akunQEl`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pivot page missing %q", want)
		}
	}
}

// TestTransactions_ErrorsRenderInline: handler-supplied date errors
// surface as an inline alert, not a silent empty state.
func TestTransactions_ErrorsRenderInline(t *testing.T) {
	t.Parallel()
	body := renderPage(t, "transactions", TransactionsData{
		Title: "Transactions", DisplayName: "Tester",
		From: "2026-12-01", To: "2026-11-30",
		Errors: []string{"From date must be on or before To date."},
	})
	if !strings.Contains(body, `role="alert"`) {
		t.Error("transactions page missing role=alert for date errors")
	}
	if !strings.Contains(body, "From date must be on or before To date.") {
		t.Error("transactions page missing the date error message")
	}
	if !strings.Contains(body, `value="2026-12-01"`) || !strings.Contains(body, `value="2026-11-30"`) {
		t.Error("transactions page did not preserve the entered range")
	}
}
