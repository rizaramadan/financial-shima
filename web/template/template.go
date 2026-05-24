// Package template owns the html/template definitions for the web layer.
// Each page is a complete document parsed into its own template — there
// is no shared "body" block (which would collide across pages in a single
// template set). Layout chrome is shared via Go string concatenation,
// keeping all template strings as Go consts (no filesystem dependency).
package template

import (
	"html/template"
	"io"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// Renderer satisfies echo.Renderer using parsed html/templates.
type Renderer struct {
	t *template.Template
}

// ThemeContextKey is the key under which the active theme ("light",
// "dark", or empty for "auto") is stored in echo.Context. Middleware
// sets it from the shima_theme cookie; the Renderer reads it to fill
// in the {{themeAttr}} placeholder on <html>.
const ThemeContextKey = "shima_theme"

func New() *Renderer {
	t := template.New("").Funcs(template.FuncMap{
		"relTime":  relativeTime,
		"money":    fmtMoney,
		"txnLabel": txnLabel,
		"txnChip":  txnChipClass,
		"txnAmt":   txnAmountClass,
		"txnSign":  txnAmountSign,
		"pct":      pctOf,
		"fmtAmt":   fmtAmtDots,
		"intRange": intRange,
		// Default no-op; per-request themeAttr is wired in Render
		// after Clone() so concurrent requests don't share state.
		"themeAttr": func() template.HTMLAttr { return "" },
	})
	template.Must(t.New("login").Parse(layoutOpen + loginBody + layoutClose))
	template.Must(t.New("verify").Parse(layoutOpen + verifyBody + layoutClose))
	template.Must(t.New("home").Parse(layoutOpen + homeBody + layoutClose))
	template.Must(t.New("notifications").Parse(layoutOpen + notificationsBody + layoutClose))
	template.Must(t.New("transactions").Parse(layoutOpen + transactionsBody + layoutClose))
	template.Must(t.New("transaction_new").Parse(layoutOpen + transactionNewBody + layoutClose))
	template.Must(t.New("pos").Parse(layoutOpen + posBody + layoutClose))
	template.Must(t.New("pos_list").Parse(layoutOpen + posListBody + layoutClose))
	template.Must(t.New("pos_new").Parse(layoutOpen + posNewBody + layoutClose))
	template.Must(t.New("spending").Parse(layoutOpen + spendingBody + layoutClose))
	template.Must(t.New("income_templates").Parse(layoutOpen + incomeTemplatesListBody + layoutClose))
	template.Must(t.New("income_template_new").Parse(layoutOpen + incomeTemplateNewBody + layoutClose))
	template.Must(t.New("income_template_detail").Parse(layoutOpen + incomeTemplateDetailBody + layoutClose))
	template.Must(t.New("income_template_preview").Parse(layoutOpen + incomeTemplatePreviewBody + layoutClose))
	template.Must(t.New("settings").Parse(layoutOpen + settingsBody + layoutClose))
	template.Must(t.New("accounts").Parse(layoutOpen + accountsBody + layoutClose))
	template.Must(t.New("account_new").Parse(layoutOpen + accountNewBody + layoutClose))
	return &Renderer{t: t}
}

// relativeTime renders a human-friendly relative timestamp ("2 minutes ago"),
// per spec §6.5 ("relative timestamp"). Stable for tests via the same
// time.Time reference points handlers pass in.
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return formatN(int(d/time.Minute), "minute") + " ago"
	case d < 24*time.Hour:
		return formatN(int(d/time.Hour), "hour") + " ago"
	case d < 7*24*time.Hour:
		return formatN(int(d/(24*time.Hour)), "day") + " ago"
	default:
		return t.Format("Jan 2")
	}
}

func formatN(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return decimalString(int64(n)) + " " + unit + "s"
}

func decimalString(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// fmtMoney formats an integer amount in the given currency for display.
// IDR has no fractional unit (1 = 1 rupiah), grouped with dot thousands —
// "Rp 1.500.000". USD/EUR are stored in cents, so we /100 and group with
// commas — "$1,500.00". Other currencies fall back to grouped digits +
// upper-case currency tag — "100 GOLD-G".
func fmtMoney(amount int64, currency string) string {
	if amount == 0 {
		switch strings.ToUpper(currency) {
		case "USD", "EUR":
			return "$0.00"
		case "IDR", "":
			return "Rp 0"
		}
		return "0 " + strings.ToUpper(currency)
	}
	abs := amount
	if abs < 0 {
		abs = -abs
	}
	sign := ""
	if amount < 0 {
		sign = "-"
	}
	switch strings.ToUpper(currency) {
	case "IDR", "":
		return sign + "Rp " + groupThousands(abs, '.')
	case "USD":
		whole := abs / 100
		cents := abs % 100
		return sign + "$" + groupThousands(whole, ',') + "." + twoDigit(cents)
	default:
		return sign + groupThousands(abs, ',') + " " + strings.ToUpper(currency)
	}
}

func fmtAmtDots(n int64) string {
	if n == 0 {
		return "0"
	}
	abs := n
	if abs < 0 {
		abs = -abs
	}
	s := groupThousands(abs, '.')
	if n < 0 {
		return "-" + s
	}
	return s
}

func groupThousands(n int64, sep byte) string {
	s := decimalString(n)
	if len(s) <= 3 {
		return s
	}
	out := make([]byte, 0, len(s)+(len(s)-1)/3)
	pre := len(s) % 3
	if pre > 0 {
		out = append(out, s[:pre]...)
	}
	for i := pre; i < len(s); i += 3 {
		if len(out) > 0 {
			out = append(out, sep)
		}
		out = append(out, s[i:i+3]...)
	}
	return string(out)
}

func twoDigit(n int64) string {
	if n < 10 {
		return "0" + decimalString(n)
	}
	return decimalString(n)
}

// Transaction type → human label + visual class. Spec stores types as
// snake_case enum strings (money_in, money_out, inter_pos); the UI shows
// them as colored chips with friendlier labels per spec §6.1.
func txnLabel(t string) string {
	switch t {
	case "money_in":
		return "Income"
	case "money_out":
		return "Expense"
	case "inter_pos":
		return "Transfer"
	}
	return t
}

func txnChipClass(t string) string {
	switch t {
	case "money_in":
		return "chip-in"
	case "money_out":
		return "chip-out"
	case "inter_pos":
		return "chip-transfer"
	}
	return "chip-neutral"
}

func txnAmountClass(t string) string {
	switch t {
	case "money_in":
		return "amt-in"
	case "money_out":
		return "amt-out"
	}
	return "amt-neutral"
}

func txnAmountSign(t string) string {
	switch t {
	case "money_in":
		return "+"
	case "money_out":
		return "−"
	}
	return ""
}

// intRange returns the half-open range [from, to) as a []int. Used by
// templates to render N identical rows (e.g. 8 empty line inputs).
func intRange(from, to int) []int {
	if to <= from {
		return nil
	}
	out := make([]int, 0, to-from)
	for i := from; i < to; i++ {
		out = append(out, i)
	}
	return out
}

// pctOf clamps to [0, 100] and rounds. Used for budget-progress rails on
// Pos rows; zero or negative target returns 0.
func pctOf(num, denom int64) int {
	if denom <= 0 {
		return 0
	}
	p := (num * 100) / denom
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return int(p)
}

// Render is the echo.Renderer entry point. We Clone() the parsed
// template tree per request so the {{themeAttr}} func can closure
// over the request-scoped theme without racing other in-flight
// requests. Cloning a parsed template is microsecond-cheap.
func (r *Renderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	theme := ""
	if c != nil {
		if v, ok := c.Get(ThemeContextKey).(string); ok {
			theme = v
		}
	}
	clone, err := r.t.Clone()
	if err != nil {
		return err
	}
	clone = clone.Funcs(template.FuncMap{
		"themeAttr": func() template.HTMLAttr {
			if theme != "light" && theme != "dark" {
				return "" // no override → CSS @media decides
			}
			return template.HTMLAttr(` data-theme="` + theme + `"`)
		},
	})
	return clone.ExecuteTemplate(w, name, data)
}

// LoginData drives the login template. Error is non-empty when the user
// just submitted an unknown identifier or hit cooldown.
type LoginData struct {
	Title string
	Error string
}

// Compact narrows the card for single-input forms (AntD form widths).
func (d LoginData) Compact() bool { return true }
func (d LoginData) Wide() bool    { return false }

// HideBell — pre-auth pages have no bell anyway (SignedIn=false), but
// satisfy the interface uniformly.
func (d LoginData) HideBell() bool { return false }

// Route — empty string suppresses the nav (pre-auth pages don't render it).
func (d LoginData) Route() string { return "" }

// VerifyData drives the OTP-entry template. Identifier round-trips so the
// hidden field can replay it on POST.
type VerifyData struct {
	Title      string
	Identifier string
	Error      string
}

func (d VerifyData) Compact() bool  { return true }
func (d VerifyData) Wide() bool     { return false }
func (d VerifyData) HideBell() bool { return false }
func (d VerifyData) Route() string  { return "" }

// NotificationsData drives the per-user feed (spec §6.5). Items are
// pre-sorted newest-first by the SQL query.
type NotificationsData struct {
	Title       string
	DisplayName string
	Items       []NotificationRow
	UnreadCount int
	LoadError   bool
}

// SignedIn reports whether to render the layout's authenticated header
// (bell badge, etc.). Logged-out templates leave DisplayName empty.
func (d NotificationsData) SignedIn() bool { return d.DisplayName != "" }

// Compact — list-style pages keep the default card width.
func (d NotificationsData) Compact() bool { return false }
func (d NotificationsData) Wide() bool    { return false }

// HideBell — the bell links to this very page; suppress it here so it
// doesn't point at itself.
func (d NotificationsData) HideBell() bool { return true }
func (d NotificationsData) Route() string  { return "notifications" }

// NotificationRow is one row in the feed.
type NotificationRow struct {
	ID           string
	Title        string
	Body         string
	HasRelated   bool
	RelatedTxnID string
	IsRead       bool
	CreatedAt    time.Time
}

// Methods on data structs the layout calls for the authenticated header.
// LoginData / VerifyData are not authenticated → SignedIn() returns false.

// HomeData drives the home view per spec §6.2 (current balances).
// Empty Accounts / PosByCurrency triggers either the placeholder fallback
// (LoadError = false: DB is unwired or empty) or an error message
// (LoadError = true: a real DB call failed).
type HomeData struct {
	Title         string
	DisplayName   string
	Today         string
	TotalIDR      int64
	Accounts      []AccountRow
	PosByCurrency []PosCurrencyGroup
	LoadError     bool
	UnreadCount   int // server-rendered bell badge (no JS, full-page poll)
}

// SignedIn for HomeData mirrors NotificationsData — the home page is only
// reachable post-auth, so a populated DisplayName is the trigger.
func (d HomeData) SignedIn() bool { return d.DisplayName != "" }
func (d HomeData) Compact() bool  { return false }
func (d HomeData) Wide() bool     { return false }
func (d HomeData) HideBell() bool { return false }
func (d HomeData) Route() string  { return "home" }

// LoginData and VerifyData are pre-auth; SignedIn always false.
func (d LoginData) SignedIn() bool  { return false }
func (d VerifyData) SignedIn() bool { return false }

// SpendingData drives the §6.4 view: months × top-N Pos pivot.
type SpendingData struct {
	Title       string
	DisplayName string
	UnreadCount int
	From        string
	To          string
	TopN        int
	Columns     []SpendingColumn // top-N pos, in rank order
	Rows        []SpendingRow    // one per month in range, newest first
	// MixedCurrency is true when the top-N columns span more than one
	// currency. Per spec §10.5 currencies reconcile separately, so a
	// cross-currency row total is meaningless — the template hides it.
	MixedCurrency bool
	LoadError     bool
}

// SignedIn — only authenticated users reach the spending view.
func (d SpendingData) SignedIn() bool { return d.DisplayName != "" }
func (d SpendingData) Compact() bool  { return false }
func (d SpendingData) Wide() bool     { return true }
func (d SpendingData) HideBell() bool { return false }
func (d SpendingData) Route() string  { return "spending" }

// SpendingColumn is one of the top-N pos.
type SpendingColumn struct {
	PosID    string
	Name     string
	Currency string
	Total    int64 // sum across the date range
}

// SpendingRow is one month, with a cell per top-N pos plus a row total.
type SpendingRow struct {
	Month string  // "Apr 2026"
	Cells []int64 // amounts in column order; zero-filled for months with no data
	Total int64
}

// PosDetailData drives the §6.3 single-Pos view. NotFound triggers a
// distinct "no such Pos" render; LoadError is the transient-DB-failure
// state. Empty Obligations + Transactions is the legitimate empty case.
type PosDetailData struct {
	Title        string
	DisplayName  string
	UnreadCount  int
	ID           string
	Name         string
	Currency     string
	AccountID    string          // current funding account
	AccountName  string          // for display next to the change form
	Accounts     []AccountOption // for the change-account <select>
	AccountFlash string          // success flash after a successful PATCH
	Target       int64
	HasTarget    bool
	Archived     bool
	Cash         int64
	Receivables  int64
	Payables     int64
	Obligations  []ObligationRow
	Transactions []PosTransactionRow
	NotFound     bool
	LoadError    bool
}

// SignedIn — only authenticated users reach pos detail.
func (d PosDetailData) SignedIn() bool { return d.DisplayName != "" }
func (d PosDetailData) Compact() bool  { return false }
func (d PosDetailData) Wide() bool     { return false }
func (d PosDetailData) HideBell() bool { return false }
func (d PosDetailData) Route() string  { return "pos" }

// PosNewData drives the "create Pos" form. Name/Currency/TargetRaw/AccountID
// round-trip on validation failure so the user doesn't retype.
type PosNewData struct {
	Title       string
	DisplayName string
	UnreadCount int
	Name        string
	Currency    string
	AccountID   string          // chosen account; required per spec §4.2
	Accounts    []AccountOption // all non-archived accounts
	TargetRaw   string          // string form so empty stays empty across re-renders
	Errors      []string        // list of validation messages, all rendered together
}

func (d PosNewData) SignedIn() bool { return d.DisplayName != "" }
func (d PosNewData) Compact() bool  { return true }
func (d PosNewData) Wide() bool     { return false }
func (d PosNewData) HideBell() bool { return false }
func (d PosNewData) Route() string  { return "home" }

// ObligationRow is one open obligation involving this Pos. Direction is
// "receivable" (this pos is creditor) or "payable" (this pos is debtor).
type ObligationRow struct {
	ID           string
	Direction    string // "receivable" | "payable"
	OtherPosID   string
	OtherPosName string // empty when handler hasn't resolved the name yet
	Currency     string
	Outstanding  int64
	CreatedAt    time.Time
}

// PosTransactionRow is one row of the scoped transaction list. Trimmer
// than TransactionRow because pos identity is implicit on this page.
type PosTransactionRow struct {
	ID               string
	Type             string
	EffectiveDate    string
	Amount           int64
	AccountName      string
	CounterpartyName string
	Note             string
	IsReversal       bool
	ReversesID       string
}

// TransactionsData drives the §6.1 list. Items are pre-sorted newest-first
// by the SQL query.
type TransactionsData struct {
	Title       string
	DisplayName string
	From        string // YYYY-MM-DD echoed back into the filter form
	To          string
	RangeLabel  string
	TotalIn     int64
	TotalOut    int64
	Net         int64
	Items       []TransactionRow
	Days        []DayGroup
	LoadError   bool
	UnreadCount int
}

type DayGroup struct {
	Date   string
	NetIn  int64
	NetOut int64
	Items  []TransactionRow
}

// SignedIn for transactions list — only reachable post-auth.
func (d TransactionsData) SignedIn() bool { return d.DisplayName != "" }
func (d TransactionsData) Compact() bool  { return false }
func (d TransactionsData) Wide() bool     { return true }
func (d TransactionsData) HideBell() bool { return false }
func (d TransactionsData) Route() string  { return "transactions" }

// TransactionNewData drives the one-off "new income" / "new spending"
// form. Type is set by the handler from the ?type= query param so the
// same template renders both with focused titles + submit labels. All
// raw form fields are kept as strings so a validation failure can
// re-render with the user's input intact.
type TransactionNewData struct {
	Title            string
	DisplayName      string
	UnreadCount      int
	Type             string // "money_in" | "money_out"
	EffectiveDate    string // YYYY-MM-DD
	AccountID        string
	PosID            string
	PosLabel         string // typeahead input — name the user typed; server resolves to PosID
	AmountRaw        string
	CounterpartyName string
	Note             string
	IdempotencyKey   string
	Accounts         []AccountOption
	PosOptions       []PosOption
	Errors           []string
}

func (d TransactionNewData) SignedIn() bool { return d.DisplayName != "" }
func (d TransactionNewData) Compact() bool  { return false }
func (d TransactionNewData) Wide() bool     { return false }
func (d TransactionNewData) HideBell() bool { return false }
func (d TransactionNewData) Route() string  { return "transactions" }
func (d TransactionNewData) IsIncoming() bool { return d.Type == "money_in" }

// TransactionRow is one row in the list, pre-flattened from the SQL join.
type TransactionRow struct {
	ID               string
	Type             string // money_in / money_out / inter_pos
	EffectiveDate    string // YYYY-MM-DD
	Amount           int64
	Currency         string
	AccountName      string
	PosName          string
	CounterpartyName string
	Note             string
	IsReversal       bool
	ReversesID       string // populated when IsReversal
}

// AccountRow is one row in the Accounts table on /. Balance is derived
// from transactions; until that path is wired, render zero.
type AccountRow struct {
	Name       string
	BalanceIDR int64 // smallest unit (rupiah cents); 0 when balance computation isn't wired
	Bank       string
}

// PosCurrencyGroup groups Pos rows by their currency for §6.2 rendering.
type PosCurrencyGroup struct {
	Currency  string
	TotalCash int64
	Items     []PosRow
}

// PosRow is one row in a per-currency Pos table.
type PosRow struct {
	ID        string // links the row to /pos/:id detail page
	Name      string
	Cash      int64 // unit = the group's currency's smallest unit; zero until wired
	Target    int64
	HasTarget bool
}

const layoutOpen = `<!doctype html>
<html lang="en"{{themeAttr}}>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>Shima &mdash; {{.Title}}</title>
<style>
/* Ant Design v5 design tokens — adapted for plain CSS (no React).
 * Source: https://ant.design/docs/spec/colors and Seed Tokens reference.
 * Primary palette: Polar Green, shifted to green-8 so primary text on
 * white meets WCAG AA (≥4.5:1). Success stays at green-6 to keep the
 * tokens semantically distinct.
 * Functional: success #52C41A, warning #FAAD14, error #FF4D4F.
 */
:root {
  /* Brand / interactive — Polar Green (deep, for legibility on white) */
  --primary:        #237804;  /* colorPrimary  (green-8, ~6.0:1 vs white) */
  --primary-hover:  #389E0D;  /* colorPrimaryHover (green-7) */
  --primary-active: #135200;  /* colorPrimaryActive (green-9) */
  --primary-bg:     #F6FFED;  /* colorPrimaryBg (green-1) */

  /* Functional */
  --success: #52C41A;
  --warning: #FAAD14;
  --error:   #FF4D4F;
  --error-bg:#FFF1F0;
  --error-border:#FFCCC7;

  /* Neutral text + surfaces (light mode; rgba alphas per AntD v5) */
  --text:           rgba(0, 0, 0, 0.88);  /* colorText */
  --text-secondary: rgba(0, 0, 0, 0.65);  /* colorTextSecondary */
  --text-tertiary:  rgba(0, 0, 0, 0.45);  /* colorTextTertiary */
  --border:         #D9D9D9;              /* colorBorder */
  --border-secondary:#F0F0F0;             /* colorBorderSecondary (table dividers) */
  --bg-container:   #FFFFFF;              /* colorBgContainer */
  --bg-page:        #F5F5F5;              /* colorBgLayout */
  --bg-elevated:    #FFFFFF;              /* colorBgElevated */
  --bg-fill:        rgba(0, 0, 0, 0.02);  /* colorFillQuaternary — softer than bg-page */

  --radius:    6px;   /* borderRadius */
  --radius-sm: 4px;   /* borderRadiusSM */
  --radius-lg: 8px;   /* borderRadiusLG */

  --shadow-sm: 0 1px 2px 0 rgba(0,0,0,0.03), 0 1px 6px -1px rgba(0,0,0,0.02), 0 2px 4px 0 rgba(0,0,0,0.02);

  --font-sm: 12px; --font-base: 14px; --font-lg: 16px;
  --font-h5: 16px; --font-h4: 20px; --font-h3: 24px; --font-h2: 30px; --font-h1: 38px;

  accent-color: var(--primary);
}
/* Dark-mode tokens — used by both the @media auto path AND the
 * explicit :root[data-theme="dark"] override below. Defined once via
 * a custom-property block we can re-apply. */
@media (prefers-color-scheme: dark) {
  :root {
    --primary:        #6ABE39;
    --primary-hover:  #8FD460;
    --primary-active: #49AA19;
    --primary-bg:     #162312;

    --error:    #DC4446;
    --error-bg: #2C1618;
    --error-border:#5C2223;

    --text:           rgba(255, 255, 255, 0.85);
    --text-secondary: rgba(255, 255, 255, 0.65);
    --text-tertiary:  rgba(255, 255, 255, 0.45);
    --border:         #424242;
    --border-secondary:#303030;
    --bg-container:   #141414;
    --bg-page:        #000000;
    --bg-elevated:    #1F1F1F;
    --bg-fill:        rgba(255, 255, 255, 0.04);
  }
}
/* Explicit user choice — beats the @media query.
 *   data-theme="light"  forces light even when OS prefers dark
 *   data-theme="dark"   forces dark even when OS prefers light
 *   no attribute        falls through to @media (= "auto" / OS) */
:root[data-theme="light"] {
  --primary:        #237804;
  --primary-hover:  #389E0D;
  --primary-active: #135200;
  --primary-bg:     #F6FFED;

  --error:    #FF4D4F;
  --error-bg: #FFF1F0;
  --error-border:#FFCCC7;

  --text:           rgba(0, 0, 0, 0.88);
  --text-secondary: rgba(0, 0, 0, 0.65);
  --text-tertiary:  rgba(0, 0, 0, 0.45);
  --border:         #D9D9D9;
  --border-secondary:#F0F0F0;
  --bg-container:   #FFFFFF;
  --bg-page:        #F5F5F5;
  --bg-elevated:    #FFFFFF;
  --bg-fill:        rgba(0, 0, 0, 0.02);
}
:root[data-theme="dark"] {
  --primary:        #6ABE39;
  --primary-hover:  #8FD460;
  --primary-active: #49AA19;
  --primary-bg:     #162312;

  --error:    #DC4446;
  --error-bg: #2C1618;
  --error-border:#5C2223;

  --text:           rgba(255, 255, 255, 0.85);
  --text-secondary: rgba(255, 255, 255, 0.65);
  --text-tertiary:  rgba(255, 255, 255, 0.45);
  --border:         #424242;
  --border-secondary:#303030;
  --bg-container:   #141414;
  --bg-page:        #000000;
  --bg-elevated:    #1F1F1F;
  --bg-fill:        rgba(255, 255, 255, 0.04);
}
::selection { background: color-mix(in oklab, var(--primary) 25%, transparent); }
* { box-sizing: border-box; }
html, body { margin: 0; padding: 0; }
/* Layout. Two body modes:
 *   .signed-in → grid with sidebar (left) + main (right). Sidebar is
 *      fixed-position on mobile and slid in via a CSS-only checkbox
 *      toggle; at ≥768px it promotes to a static grid column.
 *   default (login / verify) → single centered column, no sidebar.
 * Mobile-first: base rules target narrow viewports; min-width queries
 * scale up for tablets (≥480px) and desktop (≥768px). */
body {
  background: var(--bg-page);
  color: var(--text);
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto,
               "Helvetica Neue", Arial, "PingFang SC", "Hiragino Sans GB",
               "Microsoft YaHei", sans-serif;
  font-size: var(--font-base); line-height: 1.5714;
  min-height: 100vh; display: grid;
  align-items: start; justify-items: center;
  padding: 0;
}
body.signed-in {
  /* Mobile: single column. Topbar row above main. */
  grid-template-columns: 1fr;
  grid-template-rows: auto 1fr;
  grid-template-areas: "topbar" "main";
  justify-items: stretch;
  padding: 0;
}
main {
  position: relative; /* anchor for the bell */
  width: 100%; max-width: 720px;
  /* Body is display:grid, so main as a grid item defaults to
   * min-width: auto (= min-content). Without this override, the
   * sticky .nav with its overflow-x:auto child cannot shrink — main
   * grows to fit the nav's full unbroken width (~486px), pushes past
   * the 375px viewport, and the browser auto-zooms-out the whole
   * page. min-width:0 lets main collapse so the nav scrolls
   * internally as designed. */
  min-width: 0;
  background: var(--bg-container);
  border-radius: 0;
  padding: 16px;
  box-shadow: var(--shadow-sm);
  border: 1px solid var(--border-secondary);
  border-left: 0; border-right: 0;
}
main.compact { max-width: 420px; }
main.wide    { max-width: 920px; }
/* Signed-in main fills the grid cell; .compact/.wide still cap form
 * pages but they centre within the cell instead of the viewport. */
body.signed-in > main {
  grid-area: main;
  max-width: none;
  border: 0; border-radius: 0; box-shadow: none;
}
body.signed-in > main.compact { max-width: 420px; margin: 0 auto; }
body.signed-in > main.wide    { max-width: 920px; margin: 0 auto; }
@media (min-width: 480px) {
  body { padding: 16px; }
  body.signed-in { padding: 0; }
  main { padding: 24px; border-radius: var(--radius-lg);
    border-left: 1px solid var(--border-secondary);
    border-right: 1px solid var(--border-secondary); }
  body.signed-in > main { border: 0; border-radius: 0; }
}
@media (min-width: 768px) {
  body { padding: 24px; }
  body.signed-in {
    padding: 0;
    grid-template-columns: 240px 1fr;
    grid-template-rows: 1fr;
    grid-template-areas: "sidebar main";
  }
  main { padding: 32px; }
  main.compact { padding: 32px 28px; }
  body.signed-in > main { padding: 32px 40px; }
}

/* ── Sidebar + topbar (signed-in only) ─────────────────────────────
 * Markup order in layoutOpen (for the CSS sibling selectors to work):
 *   <input #sidebar-toggle> .topbar .sidebar-overlay .sidebar <main>
 * The checkbox is visually hidden; the hamburger label flips its
 * :checked state, which CSS uses to translate the sidebar into view
 * and reveal the overlay. Page nav is a full navigation (server
 * rendered) so the checkbox resets on every link click — no JS needed
 * to "auto-close" on navigation. */
.sidebar-toggle { position: absolute; opacity: 0; pointer-events: none; }
.topbar {
  grid-area: topbar;
  display: flex; align-items: center; gap: 12px;
  padding: 8px 16px;
  background: var(--bg-container);
  border-bottom: 1px solid var(--border-secondary);
  position: sticky; top: 0; z-index: 10;
}
.hamburger {
  display: inline-flex; align-items: center; justify-content: center;
  font-size: 22px; line-height: 1;
  width: 40px; height: 40px;
  border-radius: var(--radius);
  cursor: pointer; color: var(--text);
  user-select: none;
  margin-left: -8px; /* tighten optical left edge against the bar */
}
.hamburger:hover { background: var(--bg-fill); }
.hamburger:focus-visible {
  outline: none;
  box-shadow: 0 0 0 2px color-mix(in oklab, var(--primary) 25%, transparent);
}
.topbar-title { font-weight: 500; color: var(--text); }
.topbar-badge {
  margin-left: auto;
}
.sidebar {
  grid-area: sidebar;
  background: var(--bg-container);
  border-right: 1px solid var(--border-secondary);
  padding: 16px 12px;
  display: flex; flex-direction: column; gap: 4px;
  /* Mobile: drawer pinned to the viewport edge, hidden until toggled. */
  position: fixed; top: 0; bottom: 0; left: 0; width: 260px;
  transform: translateX(-100%);
  transition: transform 0.2s ease;
  z-index: 30;
  overflow-y: auto;
}
.sidebar-toggle:checked ~ .sidebar { transform: translateX(0); }
.sidebar-overlay {
  display: none;
  position: fixed; inset: 0;
  background: rgba(0, 0, 0, 0.4);
  z-index: 20;
  cursor: pointer;
}
.sidebar-toggle:checked ~ .sidebar-overlay { display: block; }
.sidebar-brand {
  font-weight: 600; color: var(--text);
  padding: 4px 12px 12px;
  border-bottom: 1px solid var(--border-secondary);
  margin: 0 0 8px;
}
.sidebar a, .sidebar form button {
  display: flex; align-items: center; gap: 8px;
  padding: 10px 12px; border-radius: var(--radius);
  color: var(--text-secondary); text-decoration: none;
  font-size: var(--font-base); white-space: nowrap;
  transition: background 0.15s, color 0.15s;
}
.sidebar a:hover { background: var(--bg-fill); color: var(--primary); }
.sidebar a[aria-current="page"] {
  background: var(--primary-bg); color: var(--primary); font-weight: 500;
}
.sidebar-end { margin-top: auto; padding-top: 12px;
  border-top: 1px solid var(--border-secondary); }
.sidebar-end .linkbtn,
.sidebar form button {
  width: 100%;
  background: transparent; border: 0; box-shadow: none;
  color: var(--text-secondary);
  text-align: left; padding: 10px 12px;
  cursor: pointer; font: inherit;
}
.sidebar-end .linkbtn:hover,
.sidebar form button:hover { background: var(--bg-fill); color: var(--primary); }
@media (min-width: 768px) {
  .topbar, .sidebar-overlay { display: none; }
  .sidebar {
    position: static; transform: none; transition: none;
    width: auto; z-index: auto;
  }
  .sidebar-toggle:checked ~ .sidebar-overlay { display: none; }
}
h1 { font-size: var(--font-h3); font-weight: 600; line-height: 1.27;
  margin: 0 0 16px; color: var(--text); }
@media (min-width: 480px) {
  h1 { font-size: var(--font-h2); line-height: 1.21; }
}
h2 { font-size: var(--font-h5); font-weight: 600; margin: 0 0 8px; color: var(--text); }

form { margin: 0; }
.field { margin-bottom: 24px; }
label { display: block; font-size: var(--font-base); font-weight: 400;
  margin-bottom: 8px; color: var(--text); }
.hint { display: block; font-size: var(--font-sm); color: var(--text-tertiary);
  margin: 4px 0 0; }

input, select {
  width: 100%; padding: 8px 12px; font: inherit; font-size: var(--font-base);
  line-height: 1.5714; color: var(--text); background: var(--bg-container);
  border: 1px solid var(--border); border-radius: var(--radius);
  transition: border-color 0.2s, box-shadow 0.2s;
}
input:hover:not(:focus) { border-color: var(--primary-hover); }
input:focus, input:focus-visible {
  outline: none; border-color: var(--primary);
  box-shadow: 0 0 0 2px color-mix(in oklab, var(--primary) 20%, transparent);
}
input::placeholder { color: var(--text-tertiary); }

/* AntD primary Button */
button {
  width: 100%; padding: 8px 16px; font: inherit; font-size: var(--font-base);
  font-weight: 400; line-height: 1.5714;
  color: #FFFFFF; background: var(--primary);
  border: 1px solid var(--primary); border-radius: var(--radius);
  cursor: pointer; transition: background 0.2s, border-color 0.2s;
  box-shadow: 0 2px 0 rgba(35, 120, 4, 0.12);
}
button:not(.linkbtn):hover:not(:disabled) { background: var(--primary-hover); border-color: var(--primary-hover); }
button:not(.linkbtn):active:not(:disabled) { background: var(--primary-active); border-color: var(--primary-active); }
button:focus-visible { outline: none; box-shadow: 0 0 0 2px color-mix(in oklab, var(--primary) 25%, transparent); }
button:disabled {
  background: var(--bg-fill); color: var(--text-tertiary);
  border-color: var(--border); cursor: not-allowed; box-shadow: none;
}

.alert {
  margin: 0 0 16px; padding: 8px 12px;
  border-radius: var(--radius);
  background: var(--error-bg); color: var(--error);
  font-size: var(--font-base); border: 1px solid var(--error-border);
}

.subtitle { margin: 0 0 24px; color: var(--text-secondary);
  font-size: var(--font-base); }
.subtitle strong { color: var(--text); font-weight: 600; }

/* AntD Link Button — Type='link' */
.linkbtn {
  display: inline; background: none; border: 0; padding: 0;
  color: var(--primary); font: inherit; font-size: var(--font-base);
  cursor: pointer; width: auto;
  transition: color 0.2s;
}
.linkbtn:hover { color: var(--primary-hover); text-decoration: underline; }
.linkbtn:active { color: var(--primary-active); }
.linkbtn:focus-visible { outline: none;
  box-shadow: 0 0 0 2px color-mix(in oklab, var(--primary) 25%, transparent);
  border-radius: var(--radius-sm); }

.aside { margin: 16px 0 0; text-align: center; font-size: var(--font-base);
  color: var(--text-tertiary); }
.aside form { display: inline; }

.card { margin: 0 0 24px; }
.card h2 { font-size: var(--font-base); font-weight: 600; margin: 0 0 12px;
  color: var(--text-tertiary); text-transform: none; letter-spacing: 0; }

/* AntD Table — tighter cell padding on mobile to fit more columns
 * before .table-wrap kicks in horizontal scroll. */
table {
  width: 100%; border-collapse: collapse;
  font-size: var(--font-base); color: var(--text);
}
thead th {
  background: var(--bg-fill); color: var(--text);
  font-weight: 500; padding: 10px 8px;
  border-bottom: 1px solid var(--border-secondary); text-align: left;
}
tbody td {
  padding: 10px 8px;
  border-bottom: 1px solid var(--border-secondary);
}
@media (min-width: 480px) {
  thead th, tbody td { padding: 12px 16px; }
}
tbody tr:hover { background: color-mix(in oklab, var(--primary) 4%, transparent); }
.num { text-align: right; font-variant-numeric: tabular-nums; }

/* AntD Badge — count pip rendered next to the Notifications nav link.
 * The nav already carries the affordance; the badge attaches an unread
 * count without duplicating the link as a separate floating bell. */
.badge {
  display: inline-flex; align-items: center; justify-content: center;
  min-width: 16px; height: 16px; padding: 0 5px; margin-left: 6px;
  border-radius: 999px;
  background: var(--error); color: #FFFFFF;
  font-size: 11px; font-weight: 600; line-height: 16px;
  font-variant-numeric: tabular-nums;
  vertical-align: middle;
}
.badge:empty { display: none; }

/* Notifications feed — on mobile, stack action below the body so the
 * "Mark read" tap target sits on its own line; ≥480px goes inline. */
.notifs { list-style: none; margin: 0; padding: 0; }
.notif {
  display: flex; flex-direction: column; gap: 8px; padding: 12px 0;
  border-bottom: 1px solid var(--border-secondary);
}
.notif:last-child { border-bottom: 0; }
.notif.unread .notif-link strong { color: var(--text); font-weight: 600; }
.notif:not(.unread) .notif-link strong { color: var(--text-secondary); font-weight: 400; }
.notif-link { flex: 1; display: block; text-decoration: none; color: inherit; }
.notif-body { display: block; font-size: var(--font-base); color: var(--text-secondary); margin-top: 4px; }
.notif-time { display: block; font-size: var(--font-sm); color: var(--text-tertiary); margin-top: 4px; }
.notif-actions { flex-shrink: 0; align-self: flex-start; }
@media (min-width: 480px) {
  .notif { flex-direction: row; gap: 12px; }
}

/* Filter row — mobile stacks each control to a comfortable touch target
 * (40px); ≥480px collapses to AntD middle size (32px) on a single row. */
.filter { display: flex; gap: 12px; align-items: end; margin: 0 0 24px; flex-wrap: wrap; }
.filter label { display: flex; flex-direction: column; gap: 4px;
  font-size: var(--font-sm); color: var(--text-tertiary);
  flex: 1 1 140px; }
.filter input { width: 100%; height: 40px; padding: 8px 12px; }
.filter button { width: 100%; height: 40px; padding: 0 16px;
  box-shadow: 0 2px 0 rgba(35, 120, 4, 0.12); }
@media (min-width: 480px) {
  .filter label { flex: 0 0 auto; }
  .filter input { width: auto; min-width: 144px; height: 32px; padding: 4px 11px; }
  .filter button { width: auto; height: 32px; }
}

/* Income-template allocation rows — what used to be a 2-col table
 * (Pos select + amount input) becomes a stack of touch-friendly rows
 * on mobile: select full-width above input full-width. ≥480px the
 * pair sits on one line. Cleaner than a cramped table on a 360px
 * phone where the select label "Mortgage (idr)" overflowed the cell. */
.alloc { display: flex; flex-direction: column; gap: 16px; margin: 0 0 16px; }
.alloc-row {
  display: flex; flex-direction: column; gap: 8px;
  padding: 12px; border: 1px solid var(--border-secondary);
  border-radius: var(--radius); background: var(--bg-fill);
}
.alloc-row select, .alloc-row input { width: 100%; min-height: 40px; }
.alloc-total {
  display: flex; justify-content: space-between; align-items: baseline;
  gap: 12px; padding: 12px; background: var(--bg-fill);
  border-radius: var(--radius); border: 1px solid var(--border-secondary);
  font-weight: 500;
}
.alloc-total strong { font-variant-numeric: tabular-nums; }
@media (min-width: 480px) {
  .alloc-row { flex-direction: row; align-items: center; gap: 12px;
    background: transparent; border: 0; padding: 0; }
  .alloc-row select { flex: 1 1 60%; min-height: 32px; }
  .alloc-row input  { flex: 1 1 40%; min-height: 32px; text-align: right;
    font-variant-numeric: tabular-nums; }
}

/* AntD Empty — icon + line for the empty content states. */
.empty-state {
  display: flex; flex-direction: column; align-items: center; text-align: center;
  padding: 48px 0; gap: 12px; color: var(--text-tertiary);
}
.empty-state svg { width: 64px; height: 41px; opacity: 0.6; }
.empty-state-text { font-size: var(--font-base); color: var(--text-secondary); margin: 0; }
.empty-state-hint { font-size: var(--font-sm); color: var(--text-tertiary); margin: 0; }

/* AntD OTP-style input — monospace, centred, generously spaced.
 * text-indent shifts glyphs to compensate for the trailing letter-spacing
 * gap, keeping the string optically centred (no asymmetric padding hack). */
.otp {
  font-family: ui-monospace, "SF Mono", Menlo, Consolas, "Courier New", monospace;
  text-align: center; letter-spacing: 0.6em; text-indent: 0.6em;
  max-width: 240px; margin: 0 auto; display: block;
  font-size: var(--font-h4);
}

.reversal td { color: var(--text-tertiary); text-decoration: line-through; }
.badge-rev {
  display: inline-block; padding: 0 8px; border-radius: var(--radius-sm);
  background: var(--bg-fill); color: var(--text-tertiary);
  font-size: var(--font-sm); font-weight: 400;
  text-decoration: none; margin-left: 8px;
  border: 1px solid var(--border-secondary);
}

/* AntD Tag — used for transaction type and obligation direction chips. */
.chip {
  display: inline-block; padding: 0 8px;
  border-radius: var(--radius-sm);
  font-size: var(--font-sm); font-weight: 500; line-height: 22px;
  border: 1px solid transparent;
  white-space: nowrap;
}
.chip-in       { color: #389E0D; background: #F6FFED; border-color: #B7EB8F; }
.chip-out      { color: #CF1322; background: #FFF1F0; border-color: #FFA39E; }
.chip-transfer { color: #0958D9; background: #E6F4FF; border-color: #91CAFF; }
.chip-neutral  { color: var(--text-secondary); background: var(--bg-fill); border-color: var(--border-secondary); }

/* Colored amounts in transaction listings — fintech standard:
 * income green (` + `), expense default (chip carries red), transfers muted. */
.amt-in      { color: #389E0D; font-weight: 500; }
.amt-out     { color: var(--text); font-weight: 500; }
.amt-neutral { color: var(--text-secondary); }

/* Wrap data-dense tables so the card width stays disciplined on narrow
 * viewports without truncating cells. */
.table-wrap { width: 100%; overflow-x: auto; margin: 0 0 8px; }

/* Pos budget progress rail — slim, sits next to the target amount on
 * /home rows. */
.progress {
  display: inline-block; vertical-align: middle;
  width: 64px; height: 6px; margin-left: 8px;
  background: var(--border-secondary);
  border-radius: 999px; overflow: hidden;
}
.progress-fill {
  display: block; height: 100%;
  background: var(--primary); border-radius: 999px;
}

/* Negative-cash marker per spec §6.2: a Pos with cash<0 carries a small
 * indicator. Non-decorative; the cell font color also flips to error. */
.neg-cash { color: var(--error); font-weight: 500; }
.neg-cash::before {
  content: "▾ "; color: var(--error);
  font-size: 11px; vertical-align: middle;
}

tr.totals { border-top: 1px solid var(--border); background: var(--bg-fill); }
tr.totals td { font-weight: 600; }

/* Mobile: horizontally scrollable tab strip — five+ items don't fit on
 * narrow screens, so let users swipe rather than wrap to multiple rows
 * (which collides with .nav-end's auto-margin). Desktop reverts to the
 * wider, non-scrolling row.
 *
 * Sticky on mobile so long pages (transactions / spending) don't
 * require scrolling back to the top to switch tabs. The negative
 * horizontal margins + matching padding bleed the sticky bg across
 * main's edge-padding so the nav never floats over visible content
 * underneath as it scrolls.
 *
 * Tap targets: padding-top + padding-bottom each 12px → ~44px nav-link
 * height including the text glyph (Apple HIG min). */
.nav {
  display: flex; gap: 16px; align-items: baseline; margin: -16px -16px 16px;
  font-size: var(--font-base);
  padding: 0 16px;
  border-bottom: 1px solid var(--border-secondary);
  overflow-x: auto;
  scrollbar-width: none;
  -webkit-overflow-scrolling: touch;
  position: sticky; top: 0; z-index: 10;
  background: var(--bg-container);
}
.nav::-webkit-scrollbar { display: none; }
.nav > * { flex-shrink: 0; }
.nav a {
  color: var(--text-secondary); text-decoration: none;
  padding: 12px 0;
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  transition: color 0.2s, border-color 0.2s;
  white-space: nowrap;
}
.nav a:hover { color: var(--primary); }
.nav a[aria-current="page"] {
  color: var(--primary); font-weight: 500;
  border-bottom-color: var(--primary);
}
.nav-end { margin-left: auto; }
.nav-end .linkbtn { color: var(--text-tertiary); padding: 12px 0; }
.nav-end .linkbtn:hover { color: var(--primary); }
@media (min-width: 480px) {
  .nav { gap: 24px; margin: -24px -24px 24px; padding: 0 24px;
    overflow-x: visible; }
}
@media (min-width: 768px) {
  .nav { margin: -32px -32px 24px; padding: 0 32px; }
}

/* Theme switcher — three side-by-side buttons; the active one
 * adopts the primary fill so the user sees their current pick.
 * Mobile stacks them full-width so each has a comfortable tap target. */
.theme-switch {
  display: flex; flex-direction: column; gap: 8px; margin: 0 0 12px;
}
.theme-switch button {
  width: 100%; padding: 10px 14px;
  background: var(--bg-container); color: var(--text);
  border: 1px solid var(--border);
  box-shadow: none;
  font-weight: 400;
}
@media (min-width: 480px) {
  .theme-switch { flex-direction: row; flex-wrap: wrap; gap: 12px; }
  .theme-switch button { width: auto; padding: 6px 14px; }
}
.theme-switch button:hover:not(.active):not(:disabled) {
  border-color: var(--primary); color: var(--primary); background: var(--bg-container);
}
.theme-switch button.active {
  background: var(--primary); color: #fff;
  border-color: var(--primary);
  font-weight: 500;
}

:root {
  --home-gradient:  linear-gradient(135deg, #14B8A6, #16A34A);
  --home-font-mono: ui-monospace, "SF Mono", Menlo, Consolas,
                    "Liberation Mono", "Courier New", monospace;
  --home-row-stripe: rgba(0, 0, 0, 0.015);
}
:root[data-theme="dark"] {
  --home-row-stripe: rgba(255, 255, 255, 0.025);
}
@media (prefers-color-scheme: dark) {
  :root { --home-row-stripe: rgba(255, 255, 255, 0.025); }
}
.home-greeting {
  display: flex; align-items: flex-end; justify-content: space-between;
  gap: 12px; margin: 0 0 20px;
}
.home-greeting h1 { margin: 0; font-size: var(--font-h3); line-height: 1.2; }
@media (min-width: 480px) {
  .home-greeting h1 { font-size: var(--font-h2); line-height: 1.21; }
}
.home-eyebrow {
  font-size: var(--font-sm); color: var(--text-tertiary);
  margin: 0 0 4px;
}
.home-avatar {
  width: 36px; height: 36px; border-radius: 99px;
  background: var(--home-gradient);
  color: #fff; font-weight: 700; font-size: 14px;
  display: inline-flex; align-items: center; justify-content: center;
  flex-shrink: 0;
}
.balance-hero {
  background: var(--bg-elevated);
  border: 1px solid var(--border-secondary);
  border-radius: var(--radius-lg);
  padding: 16px 18px;
  margin: 0 0 16px;
  display: grid; gap: 14px;
  position: relative; overflow: hidden;
}
.balance-hero::before {
  content: ""; position: absolute; left: 0; top: 0; bottom: 0; width: 3px;
  background: var(--home-gradient);
}
.balance-hero-label {
  font-family: var(--home-font-mono); font-size: 11px; font-weight: 600;
  text-transform: uppercase; letter-spacing: 0.08em;
  color: var(--text-tertiary); margin: 0 0 6px;
}
.balance-hero-amount {
  margin: 0; line-height: 1;
  font-size: 30px; font-weight: 700; letter-spacing: -0.02em;
  color: var(--text); font-variant-numeric: tabular-nums;
}
@media (min-width: 480px) {
  .balance-hero-amount { font-size: 36px; }
}
.balance-hero-meta {
  margin: 8px 0 0; font-size: var(--font-sm); color: var(--text-tertiary);
}
.balance-hero-meta strong {
  color: var(--text-secondary); font-weight: 600;
}
.balance-hero-actions {
  display: flex; flex-wrap: wrap; gap: 8px;
}
@media (min-width: 480px) {
  .balance-hero { grid-template-columns: 1fr auto; align-items: end; }
  .balance-hero-actions { justify-content: flex-end; }
}
.balance-hero-actions .pill {
  display: inline-flex; align-items: center; gap: 4px;
  padding: 7px 12px; font-size: 12px; font-weight: 600;
  color: var(--text); background: var(--bg-container);
  border: 1px solid var(--border); border-radius: var(--radius);
  text-decoration: none; white-space: nowrap;
  transition: border-color 0.15s, color 0.15s, background 0.15s;
}
.balance-hero-actions .pill:hover {
  color: var(--primary); border-color: var(--primary);
}
.balance-hero-actions .pill.primary {
  color: #fff; background: var(--primary); border-color: var(--primary);
}
.balance-hero-actions .pill.primary:hover {
  background: var(--primary-hover); border-color: var(--primary-hover);
}
.section-card {
  background: var(--bg-elevated);
  border: 1px solid var(--border-secondary);
  border-radius: var(--radius-lg);
  margin: 0 0 16px;
  overflow: hidden;
}
.section-card-head {
  display: flex; align-items: center; justify-content: space-between;
  gap: 12px; padding: 12px 16px;
  border-bottom: 1px solid var(--border-secondary);
}
.section-card-title {
  font-size: var(--font-base); font-weight: 600; color: var(--text);
}
.section-card-meta {
  font-family: var(--home-font-mono); font-size: 11px;
  color: var(--text-tertiary); white-space: nowrap;
}
.section-card-head a.linkbtn { font-size: var(--font-sm); }
.section-card table { margin: 0; }
.section-card thead th {
  background: var(--home-row-stripe);
  font-family: var(--home-font-mono);
  font-size: 10px; font-weight: 700;
  text-transform: uppercase; letter-spacing: 0.08em;
  color: var(--text-tertiary);
  padding: 8px 16px;
  border-bottom: 1px solid var(--border-secondary);
}
.section-card tbody td { padding: 12px 16px; }
.section-card tbody tr:first-child td { border-top: 0; }
.bank-cell { display: inline-flex; align-items: center; gap: 10px; }
.bank-dot {
  width: 8px; height: 8px; border-radius: 2px;
  background: var(--text-tertiary); flex-shrink: 0;
}
.bank-dot[data-bank="bca"]      { background: #0066B3; }
.bank-dot[data-bank="mandiri"]  { background: #003D7A; }
.bank-dot[data-bank="bni"]      { background: #F37021; }
.bank-dot[data-bank="bri"]      { background: #00529C; }
.bank-dot[data-bank="cimb"]     { background: #C8102E; }
.bank-dot[data-bank="permata"]  { background: #00A19A; }
.bank-dot[data-bank="cash"]     { background: var(--primary); }
.target-cell { display: inline-flex; align-items: center; gap: 10px; justify-content: flex-end; }
.target-cell .amt { font-variant-numeric: tabular-nums; color: var(--text-secondary); }
.progress-inline {
  width: 64px; height: 4px;
  background: var(--border-secondary);
  border-radius: 99px; overflow: hidden; display: inline-block;
}
.progress-inline > span {
  display: block; height: 100%;
  background: var(--primary);
  transition: width 0.3s ease;
}
.target-cell .dash { color: var(--text-tertiary); }
.section-card .empty-state { padding: 32px 16px; margin: 0; }

.page-head {
  display: flex; align-items: flex-end; justify-content: space-between;
  gap: 12px; margin: 0 0 20px; flex-wrap: wrap;
}
.page-head h1 {
  margin: 0; font-size: var(--font-h3); line-height: 1.2; font-weight: 600;
}
@media (min-width: 480px) {
  .page-head h1 { font-size: var(--font-h2); line-height: 1.21; }
}
.page-head-actions { display: flex; gap: 8px; flex-wrap: wrap; }
.page-head-actions .pill {
  display: inline-flex; align-items: center; gap: 4px;
  padding: 7px 12px; font-size: 12px; font-weight: 600;
  color: var(--text); background: var(--bg-container);
  border: 1px solid var(--border); border-radius: var(--radius);
  text-decoration: none; white-space: nowrap;
  transition: border-color 0.15s, color 0.15s, background 0.15s;
}
.page-head-actions .pill:hover { color: var(--primary); border-color: var(--primary); }
.page-head-actions .pill.primary {
  color: #fff; background: var(--primary); border-color: var(--primary);
}
.page-head-actions .pill.primary:hover {
  background: var(--primary-hover); border-color: var(--primary-hover);
}
.stats-row {
  display: grid; gap: 10px;
  grid-template-columns: 1fr; margin: 0 0 16px;
}
@media (min-width: 480px) {
  .stats-row { grid-template-columns: repeat(3, 1fr); gap: 12px; }
}
.stat-card {
  background: var(--bg-elevated);
  border: 1px solid var(--border-secondary);
  border-radius: var(--radius-lg);
  padding: 14px 16px;
  position: relative; overflow: hidden;
}
.stat-card::before {
  content: ""; position: absolute; left: 0; top: 0; bottom: 0; width: 3px;
}
.stat-card.in::before  { background: var(--primary); }
.stat-card.out::before { background: var(--error); }
.stat-card.net::before { background: var(--home-gradient); }
.stat-label {
  font-family: var(--home-font-mono); font-size: 10px; font-weight: 700;
  text-transform: uppercase; letter-spacing: 0.08em;
  color: var(--text-tertiary); margin: 0 0 6px;
}
.stat-amount {
  margin: 0; font-size: 22px; font-weight: 700; line-height: 1;
  letter-spacing: -0.01em; font-variant-numeric: tabular-nums;
  color: var(--text);
}
.stat-card.in  .stat-amount { color: #389E0D; }
.stat-card.out .stat-amount { color: var(--error); }
:root[data-theme="dark"] .stat-card.in .stat-amount { color: #95DE64; }
.stat-meta { margin: 6px 0 0; font-size: 11px; color: var(--text-tertiary); }
@media (min-width: 480px) {
  .stat-amount { font-size: 24px; }
}
.txn-filter {
  background: var(--bg-elevated);
  border: 1px solid var(--border-secondary);
  border-radius: var(--radius-lg);
  padding: 12px 14px;
  margin: 0 0 16px;
  display: flex; flex-wrap: wrap; align-items: center;
  gap: 10px;
}
.txn-filter-label {
  font-family: var(--home-font-mono); font-size: 10px; font-weight: 700;
  text-transform: uppercase; letter-spacing: 0.08em;
  color: var(--text-tertiary);
}
.txn-filter-dates {
  display: inline-flex; align-items: center; gap: 6px;
  background: var(--bg-container);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 4px 8px;
  flex-wrap: wrap;
}
.txn-filter-dates input[type="date"] {
  border: 0; padding: 2px 4px; background: transparent;
  font-size: var(--font-base); color: var(--text); width: auto;
  min-width: 110px;
}
.txn-filter-dates input[type="date"]:focus { box-shadow: none; outline: none; }
.txn-filter-dates .sep { color: var(--text-tertiary); font-size: 12px; }
.txn-filter button[type="submit"] {
  width: auto; padding: 6px 14px; height: 34px;
  font-size: 13px;
}
.txn-presets { display: inline-flex; gap: 6px; flex-wrap: wrap; }
.txn-preset {
  display: inline-flex; align-items: center;
  padding: 5px 10px; font-size: 12px; font-weight: 500;
  color: var(--text-secondary); background: var(--bg-container);
  border: 1px solid var(--border-secondary); border-radius: 99px;
  text-decoration: none;
  transition: border-color 0.15s, color 0.15s;
}
.txn-preset:hover { color: var(--primary); border-color: var(--primary); }
.txn-preset.active {
  color: var(--primary); border-color: var(--primary); background: var(--primary-bg);
}
.section-card .col-date { white-space: nowrap; font-variant-numeric: tabular-nums; color: var(--text-secondary); }
.section-card .col-amount { font-variant-numeric: tabular-nums; }
.day-strip td {
  background: var(--home-row-stripe);
  font-family: var(--home-font-mono); font-size: 10px; font-weight: 700;
  text-transform: uppercase; letter-spacing: 0.08em;
  color: var(--text-tertiary);
  padding: 8px 16px !important;
  border-top: 1px solid var(--border-secondary);
  border-bottom: 1px solid var(--border-secondary);
}
.day-strip .day-net { float: right; color: var(--text-secondary); }
.day-strip .day-net.in  { color: #389E0D; }
.day-strip .day-net.out { color: var(--error); }
:root[data-theme="dark"] .day-strip .day-net.in { color: #95DE64; }
.txn-list { display: none; padding: 4px; }
.txn-item {
  padding: 12px 14px;
  border-bottom: 1px solid var(--border-secondary);
  display: grid; gap: 6px;
  grid-template-columns: 1fr auto;
}
.txn-item:last-child { border-bottom: 0; }
.txn-item-top {
  grid-column: 1 / -1;
  display: flex; align-items: center; gap: 8px; justify-content: space-between;
}
.txn-item-chip { display: inline-flex; align-items: center; gap: 8px; }
.txn-item-date {
  font-family: var(--home-font-mono); font-size: 11px; color: var(--text-tertiary);
}
.txn-item-amount {
  font-size: 15px; font-weight: 700; font-variant-numeric: tabular-nums;
}
.txn-item-amount.in  { color: #389E0D; }
.txn-item-amount.out { color: var(--error); }
:root[data-theme="dark"] .txn-item-amount.in { color: #95DE64; }
.txn-item-amount.neutral { color: var(--text); }
.txn-item-meta {
  grid-column: 1 / -1;
  font-size: 12px; color: var(--text-secondary);
  display: flex; flex-wrap: wrap; gap: 4px 10px;
}
.txn-item-meta .sep { color: var(--text-tertiary); }
.txn-item-note {
  grid-column: 1 / -1; font-size: 12px; color: var(--text-tertiary);
  font-style: italic;
}
@media (max-width: 479px) {
  .section-card.has-card-list .table-wrap { display: none; }
  .section-card.has-card-list .txn-list   { display: block; }
}
</style>
</head>
<body{{if .SignedIn}} class="signed-in"{{end}}>
{{if .SignedIn}}
<input type="checkbox" id="sidebar-toggle" class="sidebar-toggle" aria-hidden="true">
<header class="topbar">
<label for="sidebar-toggle" class="hamburger" role="button" tabindex="0" aria-label="Toggle navigation">☰</label>
<span class="topbar-title">Shima &mdash; {{.Title}}</span>
<a href="/notifications" class="topbar-badge" aria-label="{{.UnreadCount}} notifications"><span class="badge">{{if .UnreadCount}}{{.UnreadCount}}{{end}}</span></a>
</header>
<label for="sidebar-toggle" class="sidebar-overlay" aria-hidden="true"></label>
<aside class="sidebar" aria-label="Primary">
<p class="sidebar-brand">Shima</p>
<a href="/"{{if eq .Route "home"}} aria-current="page"{{end}}>Home</a>
<a href="/transactions"{{if eq .Route "transactions"}} aria-current="page"{{end}}>Transactions</a>
<a href="/spending"{{if eq .Route "spending"}} aria-current="page"{{end}}>Spending</a>
<a href="/income-templates"{{if eq .Route "income"}} aria-current="page"{{end}}>Income</a>
<a href="/accounts"{{if eq .Route "accounts"}} aria-current="page"{{end}}>Accounts</a>
<a href="/pos"{{if eq .Route "pos"}} aria-current="page"{{end}}>Pos</a>
<a href="/notifications"{{if eq .Route "notifications"}} aria-current="page"{{end}}>Notifications<span class="badge" aria-label="{{.UnreadCount}} unread">{{if .UnreadCount}}{{.UnreadCount}}{{end}}</span></a>
<div class="sidebar-end">
<a href="/settings"{{if eq .Route "settings"}} aria-current="page"{{end}}>⚙ Settings</a>
<form method="post" action="/logout">
<button type="submit">Sign out</button>
</form>
</div>
</aside>
{{end}}
<main{{if .Compact}} class="compact"{{else if .Wide}} class="wide"{{end}}>
`

const layoutClose = `
</main>
</body>
</html>`

const loginBody = `<h1>Sign in</h1>
{{if .Error}}<p class="alert" role="alert">{{.Error}}</p>{{end}}
<form method="post" action="/login">
<div class="field">
<label for="identifier">Telegram</label>
<input id="identifier" name="identifier" inputmode="text"
  placeholder="@shima or 123456789"
  autocomplete="off" autocapitalize="off" autocorrect="off" spellcheck="false"
  required aria-describedby="identifier-hint">
<p id="identifier-hint" class="hint">@username or numeric ID</p>
</div>
<div class="field">
<label for="password">Password</label>
<input id="password" name="password" type="password"
  autocomplete="current-password" autocapitalize="off" autocorrect="off" spellcheck="false"
  required>
</div>
<button type="submit">Sign in</button>
</form>`

const verifyBody = `<h1>Enter your code</h1>
<p class="subtitle">Sent to <strong>{{.Identifier}}</strong> on Telegram. Code expires in 5 minutes.</p>
{{if .Error}}<p class="alert" role="alert">{{.Error}}</p>{{end}}
<form method="post" action="/verify">
<input type="hidden" name="identifier" value="{{.Identifier}}">
<div class="field">
<label for="code">6-digit code</label>
<input id="code" name="code" class="otp" inputmode="numeric"
  pattern="[0-9]{6}" maxlength="6" minlength="6"
  autocapitalize="off" autocorrect="off" spellcheck="false"
  required autofocus>
</div>
<button type="submit">Verify</button>
</form>
<p class="aside">
<form method="post" action="/login">
<input type="hidden" name="identifier" value="{{.Identifier}}">
<button type="submit" class="linkbtn">Send a new code</button>
</form>
&nbsp;·&nbsp;
<a class="linkbtn" href="/login">Use a different identifier</a>
</p>`

const notificationsBody = `<header class="page-head">
  <div>
    <p class="home-eyebrow">Activity</p>
    <h1>Notifications</h1>
  </div>
  {{if .UnreadCount}}<div class="page-head-actions">
    <form method="post" action="/notifications/mark-all-read">
      <button type="submit" class="pill">Mark all read ({{.UnreadCount}})</button>
    </form>
  </div>{{end}}
</header>

{{if .LoadError}}
<p class="alert" role="alert">Couldn&rsquo;t load notifications. Refresh in a moment.</p>
{{else if not .Items}}
<div class="section-card">
<div class="empty-state">
<svg viewBox="0 0 64 41" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
<ellipse cx="32" cy="33" rx="32" ry="7" fill="currentColor" opacity="0.08"/>
<path d="M55 12.76L44.85 1.18C44.24 0.43 43.36 0 42.43 0H21.57c-0.93 0-1.81 0.43-2.42 1.18L9 12.76V22h46V12.76z"
      stroke="currentColor" stroke-width="1" fill="none" opacity="0.5"/>
<path d="M41.61 16.3c0-1.94 1.39-3.52 3.1-3.52H55v18.69C55 33.95 53.07 36 50.69 36H13.31C10.93 36 9 33.95 9 31.47V12.78h10.29c1.71 0 3.1 1.58 3.1 3.51v0.05c0 1.94 1.41 3.5 3.12 3.5h12.98c1.71 0 3.12-1.57 3.12-3.51v-0.04z"
      fill="currentColor" opacity="0.15"/>
</svg>
<p class="empty-state-text">Nothing to read.</p>
</div>
</div>
{{else}}
<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Recent</span>
    <span class="section-card-meta">{{len .Items}} items</span>
  </div>
  <ul class="notifs">
  {{range .Items}}
  <li class="notif{{if not .IsRead}} unread{{end}}">
  {{if .HasRelated}}
  <a class="notif-link" href="/transactions/{{.RelatedTxnID}}">
    <strong>{{.Title}}</strong>
    {{if .Body}}<span class="notif-body">{{.Body}}</span>{{end}}
    <span class="notif-time">{{relTime .CreatedAt}}</span>
  </a>
  {{else}}
  <div class="notif-link">
    <strong>{{.Title}}</strong>
    {{if .Body}}<span class="notif-body">{{.Body}}</span>{{end}}
    <span class="notif-time">{{relTime .CreatedAt}}</span>
  </div>
  {{end}}
  {{if not .IsRead}}
  <form method="post" action="/notifications/{{.ID}}/read" class="notif-actions">
  <button type="submit" class="linkbtn">Mark read</button>
  </form>
  {{end}}
  </li>
  {{end}}
  </ul>
</section>
{{end}}`

const spendingBody = `<header class="page-head">
  <div>
    <p class="home-eyebrow">Analytics</p>
    <h1>Spending</h1>
  </div>
</header>

<form method="get" action="/spending" class="txn-filter">
  <span class="txn-filter-label">Range</span>
  <span class="txn-filter-dates">
    <input type="date" name="from" value="{{.From}}" aria-label="From">
    <span class="sep">&rarr;</span>
    <input type="date" name="to"   value="{{.To}}"   aria-label="To">
  </span>
  <button type="submit">Filter</button>
</form>

{{if .LoadError}}
<p class="alert" role="alert">Couldn&rsquo;t load spending. Refresh in a moment.</p>
{{else if not .Columns}}
<div class="section-card">
<div class="empty-state">
<svg viewBox="0 0 64 41" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
<ellipse cx="32" cy="33" rx="32" ry="7" fill="currentColor" opacity="0.08"/>
<rect x="14" y="6" width="36" height="24" rx="2" stroke="currentColor" stroke-width="1" fill="none" opacity="0.5"/>
<rect x="20" y="20" width="4" height="6" fill="currentColor" opacity="0.25"/>
<rect x="28" y="14" width="4" height="12" fill="currentColor" opacity="0.25"/>
<rect x="36" y="10" width="4" height="16" fill="currentColor" opacity="0.25"/>
<rect x="44" y="22" width="4" height="4" fill="currentColor" opacity="0.25"/>
</svg>
<p class="empty-state-text">No spending in this range.</p>
<p class="empty-state-hint">Adjust the filter or check back after the next sync.</p>
</div>
</div>
{{else}}
<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Top {{.TopN}} Pos</span>
    <span class="section-card-meta">by spending in range</span>
  </div>
  <div class="table-wrap">
  <table>
  <thead>
  <tr>
  <th>Pos</th>
  {{range .Rows}}<th class="num">{{.Month}}</th>{{end}}
  <th class="num">Pos total</th>
  </tr>
  </thead>
  <tbody>
  {{range $i, $col := .Columns}}
  <tr>
  <td><a href="/pos/{{$col.PosID}}">{{$col.Name}}</a></td>
  {{range $.Rows}}<td class="num">{{$c := index .Cells $i}}{{if $c}}{{money $c $col.Currency}}{{else}}&mdash;{{end}}</td>{{end}}
  <td class="num"><strong>{{money $col.Total $col.Currency}}</strong></td>
  </tr>
  {{end}}
  <tr class="totals">
  <td><strong>Month total</strong></td>
  {{range .Rows}}<td class="num">{{if $.MixedCurrency}}&mdash;{{else}}<strong>{{money .Total (index $.Columns 0).Currency}}</strong>{{end}}</td>{{end}}
  <td class="num">&mdash;</td>
  </tr>
  </tbody>
  </table>
  </div>
</section>
{{end}}`

const posBody = `{{if .NotFound}}
<h1>Pos not found</h1>
<p class="subtitle">No Pos with that id, or it has been removed.</p>
{{else}}
<h1>{{.Name}}{{if .Archived}} <span class="badge-rev">archived</span>{{end}}</h1>
<p class="subtitle">{{.Currency}}{{if .HasTarget}} &middot; target {{money .Target .Currency}}{{end}}</p>

{{if .LoadError}}
<p class="alert" role="alert">Some data could not be loaded. The view may be incomplete.</p>
{{end}}

<section class="card">
<h2>Balance</h2>
<table>
<thead><tr><th>Cash</th><th class="num">Receivables</th><th class="num">Payables</th></tr></thead>
<tbody>
<tr>
<td class="num{{if lt .Cash 0}} neg-cash{{end}}">{{money .Cash .Currency}}</td>
<td class="num">{{money .Receivables .Currency}}</td>
<td class="num">{{money .Payables .Currency}}</td>
</tr>
</tbody>
</table>
</section>

<section class="card">
<h2>Funding account</h2>
{{if .AccountFlash}}<p class="success" role="status">{{.AccountFlash}}</p>{{end}}
<p class="subtitle">Currently held in <strong>{{.AccountName}}</strong>. Reassigning has snapshot semantics: per-account balances update retroactively, but this Pos's history is preserved.</p>
<form method="post" action="/pos/{{.ID}}/account">
<div class="field">
<label for="change_account_id">Move to</label>
<select id="change_account_id" name="account_id" required>
{{range .Accounts}}<option value="{{.ID}}" {{if eq $.AccountID .ID}}selected{{end}}>{{.Name}}</option>
{{end}}</select>
</div>
<button type="submit">Save</button>
</form>
</section>

{{if not .Archived}}
<section class="card">
<h2>Edit</h2>
<form method="post" action="/pos/{{.ID}}/rename">
<div class="field">
<label for="rename_name">Name</label>
<input id="rename_name" name="name" type="text" value="{{.Name}}" required maxlength="80">
</div>
<div class="field">
<label for="rename_target">Target <span style="color:var(--text-tertiary); font-weight:400;">(optional, smallest unit; leave blank to clear)</span></label>
<input id="rename_target" name="target" type="text" inputmode="numeric" pattern="[0-9.,]*" maxlength="16" value="{{if .HasTarget}}{{.Target}}{{end}}">
</div>
<p class="hint">Currency ({{.Currency}}) is fixed; archive and recreate this Pos to change it.</p>
<button type="submit">Save changes</button>
</form>
</section>

<section class="card">
<h2>Archive</h2>
<p class="subtitle">Archived Pos disappear from the home view but their history is preserved. Re-create or unarchive via SQL if needed.</p>
<form method="post" action="/pos/{{.ID}}/archive" onsubmit="return confirm('Archive this Pos? It will hide from default views but past transactions stay.');">
<button type="submit">Archive this Pos</button>
</form>
</section>
{{end}}

{{if .Obligations}}
<section class="card">
<h2>Open obligations</h2>
<div class="table-wrap">
<table>
<thead><tr><th>Direction</th><th>Counterparty Pos</th><th class="num">Outstanding</th><th>Since</th></tr></thead>
<tbody>
{{range .Obligations}}
<tr>
<td><span class="chip {{if eq .Direction "receivable"}}chip-in{{else}}chip-out{{end}}">{{.Direction}}</span></td>
<td><a href="/pos/{{.OtherPosID}}">{{if .OtherPosName}}{{.OtherPosName}}{{else}}{{.OtherPosID}}{{end}}</a></td>
<td class="num">{{money .Outstanding .Currency}}</td>
<td>{{relTime .CreatedAt}}</td>
</tr>
{{end}}
</tbody>
</table>
</div>
</section>
{{end}}

{{if .Transactions}}
<section class="card">
<h2>Transactions</h2>
<div class="table-wrap">
<table>
<thead><tr><th>Date</th><th>Type</th><th class="num">Amount</th><th>Account</th><th>Counterparty</th><th>Note</th></tr></thead>
<tbody>
{{range .Transactions}}
<tr{{if .IsReversal}} class="reversal"{{end}}>
<td>{{.EffectiveDate}}</td>
<td><span class="chip {{txnChip .Type}}">{{txnLabel .Type}}</span>{{if .IsReversal}} <a class="badge-rev" href="/transactions/{{.ReversesID}}">reverses</a>{{end}}</td>
<td class="num {{txnAmt .Type}}">{{txnSign .Type}}{{money .Amount $.Currency}}</td>
<td>{{if .AccountName}}{{.AccountName}}{{else}}&mdash;{{end}}</td>
<td>{{if .CounterpartyName}}{{.CounterpartyName}}{{else}}&mdash;{{end}}</td>
<td>{{.Note}}</td>
</tr>
{{end}}
</tbody>
</table>
</div>
</section>
{{else}}
<p class="subtitle">No transactions for this Pos yet.</p>
{{end}}
{{end}}`

// PosManageRow is a row on /pos (manage list). Drives the inline
// change-account form; archived rows hide the archive button.
type PosManageRow struct {
	ID          string
	Name        string
	Currency    string
	AccountID   string
	AccountName string
	HasTarget   bool
	Target      int64
	Archived    bool
}

// PosListData drives /pos — the manage list view.
type PosListData struct {
	Title       string
	DisplayName string
	UnreadCount int
	Poses       []PosManageRow
	Accounts    []AccountOption
	Flash       string
	Error       string
}

func (d PosListData) SignedIn() bool { return d.DisplayName != "" }
func (d PosListData) Compact() bool  { return false }
func (d PosListData) Wide() bool     { return false }
func (d PosListData) HideBell() bool { return false }
func (d PosListData) Route() string  { return "pos" }

const posListBody = `<header class="page-head">
  <div>
    <p class="home-eyebrow">Budget envelopes</p>
    <h1>Pos</h1>
  </div>
  <div class="page-head-actions">
    <a class="pill primary" href="/pos/new">+ New Pos</a>
  </div>
</header>

{{if .Flash}}<p class="success" role="status">{{.Flash}}</p>{{end}}
{{if .Error}}<p class="alert" role="alert">{{.Error}}</p>{{end}}
{{if not .Poses}}
<div class="section-card">
<div class="empty-state">
<p class="empty-state-text">No Pos yet.</p>
<p class="empty-state-hint">Create one to start budgeting.</p>
</div>
</div>
{{else}}
<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">All Pos</span>
    <span class="section-card-meta">{{len .Poses}} items</span>
  </div>
  <div class="table-wrap">
  <table>
  <thead><tr><th>Name</th><th>Currency</th><th>Account</th><th>Status</th><th>Actions</th></tr></thead>
  <tbody>
  {{range .Poses}}
  <tr>
  <td><a href="/pos/{{.ID}}">{{.Name}}</a>{{if .HasTarget}} <span class="subtitle">&middot; target {{money .Target .Currency}}</span>{{end}}</td>
  <td>{{.Currency}}</td>
  <td>
    <form method="post" action="/pos/{{.ID}}/account" style="display:flex; gap:8px; align-items:center; min-width:240px;">
      <input type="hidden" name="back" value="list">
      <select name="account_id" required style="flex:1 1 auto; min-width:0;">
        {{$cur := .AccountID}}
        {{range $.Accounts}}<option value="{{.ID}}"{{if eq .ID $cur}} selected{{end}}>{{.Name}}</option>{{end}}
      </select>
      <button type="submit" style="white-space:nowrap;">Save</button>
    </form>
  </td>
  <td>{{if .Archived}}<span class="chip">archived</span>{{else}}<span class="chip chip-in">active</span>{{end}}</td>
  <td><div style="display:flex; gap:6px; flex-wrap:wrap;">
  {{if not .Archived}}
    <form method="post" action="/pos/{{.ID}}/archive" onsubmit="return confirm('Archive this Pos? It will be hidden from default lists.');" style="display:inline;">
      <button type="submit">Archive</button>
    </form>
  {{end}}
  </div></td>
  </tr>
  {{end}}
  </tbody>
  </table>
  </div>
</section>
{{end}}`

const posNewBody = `<h1>New Pos</h1>
<p class="subtitle">A Pos is a budget envelope. Money flows into it (income, transfers) and out of it (expenses).</p>
{{if .Errors}}
<div class="alert" role="alert">
<strong>Couldn&rsquo;t save this Pos:</strong>
<ul style="margin:8px 0 0 20px; padding:0;">
{{range .Errors}}<li>{{.}}</li>{{end}}
</ul>
</div>
{{end}}
<form method="post" action="/pos">
<div class="field">
<label for="name">Name</label>
<input id="name" name="name" type="text" value="{{.Name}}"
  autocapitalize="words" autocorrect="off" spellcheck="false"
  required maxlength="80"
  placeholder="e.g. Mortgage, Anak Sekolah, Liburan">
</div>
<div class="field">
<label for="currency">Currency</label>
<input id="currency" name="currency" type="text" value="{{if .Currency}}{{.Currency}}{{else}}idr{{end}}"
  autocapitalize="off" autocorrect="off" spellcheck="false"
  required maxlength="16" pattern="[a-z0-9-]+"
  placeholder="idr, usd, gold-g">
<p class="hint">Lowercase letters, digits, hyphen. Example: idr · usd · gold-g.</p>
</div>
<div class="field">
<label for="pos_account_id">Funding account</label>
<select id="pos_account_id" name="account_id" required>
<option value="" {{if not .AccountID}}selected{{end}} disabled>Choose an account…</option>
{{range .Accounts}}<option value="{{.ID}}" {{if eq $.AccountID .ID}}selected{{end}}>{{.Name}}</option>
{{end}}</select>
<p class="hint">The IDR account that funds this Pos. Required for every Pos, including non-IDR (gold-g, USD) — it's the IDR backing. You can move the Pos later (the per-account view updates retroactively).</p>
</div>
<div class="field">
<label for="target">Target <span style="color:var(--text-tertiary); font-weight:400;">(optional)</span></label>
<input id="target" name="target" type="text" inputmode="numeric"
  value="{{.TargetRaw}}"
  autocapitalize="off" autocorrect="off" spellcheck="false"
  pattern="[0-9.,]*" maxlength="16"
  placeholder="e.g. 12.000.000">
<p class="hint">Whole number in the smallest unit (rupiah for IDR, cents for USD). Leave blank for an open-ended Pos.</p>
</div>
<button type="submit">Create Pos</button>
</form>
<p class="aside"><a class="linkbtn" href="/">&larr; Cancel</a></p>`

const transactionsBody = `<header class="page-head">
  <div>
    <p class="home-eyebrow">Money &middot; ledger</p>
    <h1>Transactions</h1>
  </div>
  <div class="page-head-actions">
    <a class="pill primary" href="/transactions/new?type=money_in">+ Income</a>
    <a class="pill" href="/transactions/new?type=money_out">+ Spending</a>
  </div>
</header>

{{if .Items}}
<div class="stats-row" aria-label="Range totals">
  <div class="stat-card in">
    <p class="stat-label">Inflow</p>
    <p class="stat-amount">+{{money .TotalIn "IDR"}}</p>
    <p class="stat-meta">{{.RangeLabel}}</p>
  </div>
  <div class="stat-card out">
    <p class="stat-label">Outflow</p>
    <p class="stat-amount">−{{money .TotalOut "IDR"}}</p>
    <p class="stat-meta">{{.RangeLabel}}</p>
  </div>
  <div class="stat-card net">
    <p class="stat-label">Net</p>
    <p class="stat-amount">{{money .Net "IDR"}}</p>
    <p class="stat-meta">{{len .Items}} transactions</p>
  </div>
</div>
{{end}}

<form method="get" action="/transactions" class="txn-filter">
  <span class="txn-filter-label">Range</span>
  <span class="txn-filter-dates">
    <input type="date" name="from" value="{{.From}}" aria-label="From">
    <span class="sep">→</span>
    <input type="date" name="to"   value="{{.To}}"   aria-label="To">
  </span>
  <button type="submit">Filter</button>
  <span class="txn-presets" aria-label="Quick ranges">
    <a class="txn-preset" href="/transactions?preset=7d">Last 7 days</a>
    <a class="txn-preset" href="/transactions?preset=30d">30 days</a>
    <a class="txn-preset" href="/transactions?preset=mtd">This month</a>
    <a class="txn-preset" href="/transactions">All</a>
  </span>
</form>

{{if .LoadError}}
<p class="alert" role="alert">Couldn&rsquo;t load transactions. Refresh in a moment.</p>
{{else if not .Items}}
<div class="section-card">
<div class="empty-state">
<svg viewBox="0 0 64 41" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
<ellipse cx="32" cy="33" rx="32" ry="7" fill="currentColor" opacity="0.08"/>
<rect x="16" y="4" width="32" height="28" rx="2" stroke="currentColor" stroke-width="1" fill="none" opacity="0.5"/>
<rect x="20" y="10" width="20" height="2" fill="currentColor" opacity="0.25"/>
<rect x="20" y="16" width="24" height="2" fill="currentColor" opacity="0.25"/>
<rect x="20" y="22" width="16" height="2" fill="currentColor" opacity="0.25"/>
</svg>
<p class="empty-state-text">No transactions in this range.</p>
<p class="empty-state-hint">Try widening the date filter, or wait for the next sync.</p>
</div>
</div>
{{else}}
<section class="section-card has-card-list">
  <div class="section-card-head">
    <span class="section-card-title">Activity</span>
    <span class="section-card-meta">{{len .Items}} items &middot; newest first</span>
  </div>

  <div class="table-wrap">
    <table>
      <thead><tr>
        <th>Date</th><th>Type</th><th class="num">Amount</th>
        <th>Account</th><th>Pos</th><th>Counterparty</th><th>Note</th>
      </tr></thead>
      <tbody>
        {{range $g := .Days}}
        <tr class="day-strip"><td colspan="7">
          {{$g.Date}}
          {{if $g.NetIn}}<span class="day-net in">+{{money $g.NetIn "IDR"}}</span>{{end}}
          {{if $g.NetOut}}<span class="day-net out">−{{money $g.NetOut "IDR"}}</span>{{end}}
        </td></tr>
        {{range $g.Items}}
        <tr{{if .IsReversal}} class="reversal"{{end}}>
          <td class="col-date">{{.EffectiveDate}}</td>
          <td><span class="chip {{txnChip .Type}}">{{txnLabel .Type}}</span>{{if .IsReversal}} <a class="badge-rev" href="/transactions/{{.ReversesID}}">reverses</a>{{end}}</td>
          <td class="num col-amount {{txnAmt .Type}}">{{txnSign .Type}}{{money .Amount .Currency}}</td>
          <td>{{if .AccountName}}<span class="bank-cell"><span class="bank-dot"></span><span>{{.AccountName}}</span></span>{{else}}&mdash;{{end}}</td>
          <td>{{if .PosName}}{{.PosName}}{{else}}&mdash;{{end}}</td>
          <td>{{if .CounterpartyName}}{{.CounterpartyName}}{{else}}&mdash;{{end}}</td>
          <td>{{.Note}}</td>
        </tr>
        {{end}}
        {{end}}
      </tbody>
    </table>
  </div>

  <div class="txn-list">
    {{range $g := .Days}}
    {{range $g.Items}}
    <div class="txn-item">
      <div class="txn-item-top">
        <span class="txn-item-chip">
          <span class="chip {{txnChip .Type}}">{{txnLabel .Type}}</span>
          <span class="txn-item-date">{{.EffectiveDate}}</span>
        </span>
        <span class="txn-item-amount {{txnAmt .Type}}">{{txnSign .Type}}{{money .Amount .Currency}}</span>
      </div>
      <div class="txn-item-meta">
        {{if .AccountName}}<span>{{.AccountName}}</span>{{end}}
        {{if and .AccountName .PosName}}<span class="sep">·</span>{{end}}
        {{if .PosName}}<span>{{.PosName}}</span>{{end}}
        {{if .CounterpartyName}}<span class="sep">·</span><span>{{.CounterpartyName}}</span>{{end}}
      </div>
      {{if .Note}}<div class="txn-item-note">{{.Note}}</div>{{end}}
    </div>
    {{end}}
    {{end}}
  </div>
</section>
{{end}}`

const transactionNewBody = `<h1>{{.Title}}</h1>
<p class="subtitle">
{{if .IsIncoming}}A one-off incoming credit (e.g. refund, gift). For a salary that fans across multiple Pos, use an <a class="linkbtn" href="/income-templates">income template</a> instead.
{{else}}A one-off expense (e.g. groceries, transport).{{end}}
</p>
{{if .Errors}}
<div class="alert" role="alert">
<strong>Couldn&rsquo;t save this transaction:</strong>
<ul style="margin:8px 0 0 20px; padding:0;">
{{range .Errors}}<li>{{.}}</li>{{end}}
</ul>
</div>
{{end}}
<form method="post" action="/transactions">
<input type="hidden" name="type" value="{{.Type}}">
<input type="hidden" name="idempotency_key" value="{{.IdempotencyKey}}">
<div class="field">
<label for="effective_date">Effective date</label>
<input id="effective_date" name="effective_date" type="date" required value="{{.EffectiveDate}}">
</div>
<div class="field">
<label for="pos_label">{{if .IsIncoming}}Destination Pos{{else}}Pos charged{{end}}</label>
<input id="pos_label" name="pos_label" type="text" required maxlength="80"
  list="pos-options" autocomplete="off"
  value="{{.PosLabel}}" placeholder="Type to filter…">
<datalist id="pos-options">
{{range .PosOptions}}<option value="{{.Name}}"></option>{{end}}
</datalist>
<p class="hint">Start typing to filter. Pick a suggestion or type the exact Pos name. IDR only here; cross-currency stays behind the API.</p>
</div>
<div class="field">
<label for="amount">Amount (IDR)</label>
<input id="amount" name="amount" type="text" inputmode="numeric" pattern="[0-9.,]*" required
  value="{{.AmountRaw}}" placeholder="e.g. 250.000">
</div>
<div class="field">
<label for="counterparty_name">Counterparty</label>
<input id="counterparty_name" name="counterparty_name" type="text" required maxlength="80"
  value="{{.CounterpartyName}}"
  placeholder="{{if .IsIncoming}}e.g. PT Telkom{{else}}e.g. Indomaret{{end}}">
<p class="hint">A new counterparty is created automatically if the name doesn&rsquo;t exist yet.</p>
</div>
<div class="field">
<label for="note">Note <span style="color:var(--text-tertiary); font-weight:400;">(optional)</span></label>
<input id="note" name="note" type="text" maxlength="200" value="{{.Note}}">
</div>
<button type="submit">{{if .IsIncoming}}Record income{{else}}Record spending{{end}}</button>
</form>
<p class="aside"><a class="linkbtn" href="/transactions">&larr; Cancel</a></p>`

const homeBody = `<header class="home-greeting">
  <div>
    {{if .Today}}<p class="home-eyebrow">{{.Today}}</p>{{end}}
    <h1>Hi, {{.DisplayName}}</h1>
  </div>
  <div class="home-avatar" aria-hidden="true">{{if .DisplayName}}{{slice .DisplayName 0 1}}{{end}}</div>
</header>

{{if .LoadError}}
<p class="alert" role="alert">Couldn&rsquo;t load your accounts and pos right now. Refresh in a moment.</p>
{{else if and (not .Accounts) (not .PosByCurrency)}}
<div class="empty-state">
<svg viewBox="0 0 64 41" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
<ellipse cx="32" cy="33" rx="32" ry="7" fill="currentColor" opacity="0.08"/>
<path d="M14 14 H50 V30 H14 Z" stroke="currentColor" stroke-width="1" fill="none" opacity="0.5"/>
<rect x="14" y="14" width="36" height="6" fill="currentColor" opacity="0.15"/>
<circle cx="42" cy="25" r="2" fill="currentColor" opacity="0.4"/>
</svg>
<p class="empty-state-text">Nothing here yet.</p>
<p class="empty-state-hint">Start by creating a Pos — your first budget envelope.</p>
<p class="empty-state-action"><a class="linkbtn" href="/pos/new">+ New Pos</a></p>
</div>
{{end}}

{{if or .Accounts .PosByCurrency}}
<section class="balance-hero" aria-label="Total balance">
  <div>
    <p class="balance-hero-label">Total balance &middot; IDR</p>
    <p class="balance-hero-amount">{{money .TotalIDR "IDR"}}</p>
    <p class="balance-hero-meta">Across <strong>{{len .Accounts}} accounts</strong>{{if .PosByCurrency}} &middot; <strong>{{len (index .PosByCurrency 0).Items}} pos in IDR</strong>{{end}}</p>
  </div>
  <div class="balance-hero-actions">
    <a class="pill primary" href="/transactions/new?type=money_in">+ Income</a>
    <a class="pill" href="/transactions/new?type=money_out">+ Spending</a>
    <a class="pill" href="/pos/new">+ New Pos</a>
  </div>
</section>
{{end}}

{{if .Accounts}}
<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Accounts</span>
    <a class="linkbtn" href="/accounts">Manage &rarr;</a>
  </div>
  <div class="table-wrap">
    <table>
      <thead><tr><th>Name</th><th class="num">Balance</th></tr></thead>
      <tbody>
        {{range .Accounts}}
        <tr>
          <td>
            <span class="bank-cell">
              <span class="bank-dot"{{if .Bank}} data-bank="{{.Bank}}"{{end}}></span>
              <span>{{.Name}}</span>
            </span>
          </td>
          <td class="num{{if lt .BalanceIDR 0}} neg-cash{{end}}">{{money .BalanceIDR "IDR"}}</td>
        </tr>
        {{end}}
      </tbody>
    </table>
  </div>
</section>
{{end}}

{{range $g := .PosByCurrency}}
<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Pos</span>
    <span class="section-card-meta">{{$g.Currency}} &middot; {{len $g.Items}} items{{if $g.TotalCash}} &middot; {{money $g.TotalCash $g.Currency}} allocated{{end}}</span>
  </div>
  <div class="table-wrap">
    <table>
      <thead><tr><th>Name</th><th class="num">Cash</th><th class="num">Target</th></tr></thead>
      <tbody>
        {{range $g.Items}}
        <tr>
          <td>{{if .ID}}<a href="/pos/{{.ID}}">{{.Name}}</a>{{else}}{{.Name}}{{end}}</td>
          <td class="num{{if lt .Cash 0}} neg-cash{{end}}">{{money .Cash $g.Currency}}</td>
          <td class="num">
            {{if .HasTarget}}
              <span class="target-cell">
                <span class="amt">{{money .Target $g.Currency}}</span>
                <span class="progress-inline" aria-label="{{pct .Cash .Target}}% of target"><span style="width: {{pct .Cash .Target}}%"></span></span>
              </span>
            {{else}}<span class="dash">&mdash;</span>{{end}}
          </td>
        </tr>
        {{end}}
      </tbody>
    </table>
  </div>
</section>
{{end}}
`

// ─── Income templates ──────────────────────────────────────────────

// PosOption is one Pos shown in a <select>. Used by the new-template
// form (line picker) and the apply form (account picker reuses
// AccountOption — kept symmetric for clarity).
type PosOption struct {
	ID       string
	Name     string
	Currency string
}

// AccountOption is one account shown in a <select> on the apply form.
type AccountOption struct {
	ID   string
	Name string
}

// IncomeTemplatesListData drives /income-templates.
type IncomeTemplatesListData struct {
	Title       string
	DisplayName string
	UnreadCount int
	Items       []IncomeTemplateRow
	LoadError   bool
}

func (d IncomeTemplatesListData) SignedIn() bool { return d.DisplayName != "" }
func (d IncomeTemplatesListData) Compact() bool  { return false }
func (d IncomeTemplatesListData) Wide() bool     { return false }
func (d IncomeTemplatesListData) HideBell() bool { return false }
func (d IncomeTemplatesListData) Route() string  { return "income" }

// IncomeTemplateRow is one row in the list.
type IncomeTemplateRow struct {
	ID    string
	Name  string
	Total int64 // Σ(lines.amount) — IDR; templates are IDR-only today
}

// IncomeTemplateNewData drives /income-templates/new. Round-trips
// the user's input on validation failure so they don't retype.
type IncomeTemplateNewData struct {
	Title         string
	DisplayName   string
	UnreadCount   int
	Pos           []PosOption
	Name          string
	LeftoverPosID string
	Lines         []IncomeTemplateLineInput
	Errors        []string
	LoadError     bool
}

func (d IncomeTemplateNewData) SignedIn() bool { return d.DisplayName != "" }
func (d IncomeTemplateNewData) Compact() bool  { return false }
func (d IncomeTemplateNewData) Wide() bool     { return false }
func (d IncomeTemplateNewData) HideBell() bool { return false }
func (d IncomeTemplateNewData) Route() string  { return "income" }

// IncomeTemplateLineInput is one row of the new-template form (raw
// strings so we can re-render unchanged on validation failure).
type IncomeTemplateLineInput struct {
	PosID  string
	Amount string
}

// IncomeTemplateDetailData drives /income-templates/:id.
type IncomeTemplateDetailData struct {
	Title           string
	DisplayName     string
	UnreadCount     int
	ID              string
	Name            string
	Lines           []IncomeTemplateLineRow
	LinesTotal      int64
	LeftoverPosID   string
	LeftoverPosName string
	HasLeftoverPos  bool
	Pos             []PosOption
	Accounts        []AccountOption
	Flash           string // surfaced after apply
}

func (d IncomeTemplateDetailData) SignedIn() bool { return d.DisplayName != "" }
func (d IncomeTemplateDetailData) Compact() bool  { return false }
func (d IncomeTemplateDetailData) Wide() bool     { return false }
func (d IncomeTemplateDetailData) HideBell() bool { return false }
func (d IncomeTemplateDetailData) Route() string  { return "income" }

// IncomeTemplateLineRow is one allocation row on the detail page.
type IncomeTemplateLineRow struct {
	PosID       string
	PosName     string
	PosCurrency string
	Amount      int64
}

// IncomeTemplatePreviewData drives /income-templates/:id (preview
// step). Pre-fills the editable allocation form with the suggested
// rows from the template; user can adjust amounts, swap Pos, or
// add/remove rows before approving.
type IncomeTemplatePreviewData struct {
	Title            string
	DisplayName      string
	UnreadCount      int
	ID               string
	TemplateName     string
	Amount           int64
	AmountRaw        string
	EffectiveDate    string
	AccountID        string
	AccountName      string
	CounterpartyName string
	IdempotencyKey   string
	PosOptions       []PosOption
	Rows             []IncomeAllocationRow
	SuggestionTotal  int64
	SuggestionNotice string // surfaced when the suggestion isn't directly approvable
}

func (d IncomeTemplatePreviewData) SignedIn() bool { return d.DisplayName != "" }
func (d IncomeTemplatePreviewData) Compact() bool  { return false }
func (d IncomeTemplatePreviewData) Wide() bool     { return false }
func (d IncomeTemplatePreviewData) HideBell() bool { return false }
func (d IncomeTemplatePreviewData) Route() string  { return "income" }

// IncomeAllocationRow is one editable preview row. PosID empty +
// Amount empty = "skip me" (lets the operator zero out a pre-filled
// row by clearing both fields).
type IncomeAllocationRow struct {
	PosID    string // selected option value
	PosLabel string // display-only convenience (e.g. "Mortgage (idr)")
	Amount   string // raw integer string so empty stays empty across re-renders
}

const incomeTemplatesListBody = `<header class="page-head">
  <div>
    <p class="home-eyebrow">Allocation</p>
    <h1>Income templates</h1>
  </div>
  <div class="page-head-actions">
    <a class="pill primary" href="/income-templates/new">+ New template</a>
  </div>
</header>

{{if .LoadError}}
<p class="alert" role="alert">Couldn&rsquo;t load templates. Refresh in a moment.</p>
{{else if not .Items}}
<div class="section-card">
<div class="empty-state">
<p class="empty-state-text">No income templates yet.</p>
<p class="empty-state-hint">Create one to fan-out a salary across Pos in one step.</p>
</div>
</div>
{{else}}
<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Templates</span>
    <span class="section-card-meta">{{len .Items}} items</span>
  </div>
  <div class="table-wrap">
  <table>
  <thead><tr><th>Name</th><th class="num">Lines total</th></tr></thead>
  <tbody>
  {{range .Items}}
  <tr><td><a href="/income-templates/{{.ID}}">{{.Name}}</a></td><td class="num">{{money .Total "idr"}}</td></tr>
  {{end}}
  </tbody>
  </table>
  </div>
</section>
{{end}}`

const incomeTemplateNewBody = `<h1>New income template</h1>
<p class="subtitle">Each line allocates a fixed amount to one Pos.</p>
{{if .Errors}}
<div class="alert" role="alert">
<strong>Couldn&rsquo;t save template:</strong>
<ul style="margin:8px 0 0 20px; padding:0;">
{{range .Errors}}<li>{{.}}</li>{{end}}
</ul>
</div>
{{end}}
<form method="post" action="/income-templates" id="tpl-form">
<input type="hidden" name="line_count" id="line-count" value="{{if .Lines}}{{len .Lines}}{{else}}8{{end}}">
<div class="field">
<label for="name">Name</label>
<input id="name" name="name" type="text" value="{{.Name}}" required maxlength="80"
  autocapitalize="words" autocorrect="off" spellcheck="false"
  placeholder="e.g. Riza monthly salary">
</div>
<div class="field">
<label for="leftover_pos_id">Leftover Pos <span style="color:var(--text-tertiary); font-weight:400;">(optional)</span></label>
<select id="leftover_pos_id" name="leftover_pos_id">
<option value="">— none (strict: amount must equal Σ(lines)) —</option>
{{range .Pos}}
<option value="{{.ID}}"{{if eq .ID $.LeftoverPosID}} selected{{end}}>{{.Name}} ({{.Currency}})</option>
{{end}}
</select>
<p class="hint">If set, any amount above the lines&rsquo; total lands here. Otherwise a too-large amount is rejected.</p>
</div>
<h2 style="font-size: var(--font-base); font-weight: 600; margin: 24px 0 12px;">Lines</h2>
<div class="alloc" id="alloc-lines">
{{range $i, $line := .Lines}}
<div class="alloc-row">
<select name="pos_id_{{$i}}" aria-label="Pos for line {{$i}}">
<option value="">— skip —</option>
{{range $.Pos}}<option value="{{.ID}}"{{if eq .ID $line.PosID}} selected{{end}}>{{.Name}} ({{.Currency}})</option>{{end}}
</select>
<input name="amount_{{$i}}" type="text" inputmode="numeric" pattern="[0-9.,]*" value="{{$line.Amount}}"
  placeholder="e.g. 12.000.000" aria-label="Amount for line {{$i}}">
</div>
{{end}}
{{if not .Lines}}
{{range $i := (intRange 0 8)}}
<div class="alloc-row">
<select name="pos_id_{{$i}}" aria-label="Pos for line {{$i}}">
<option value="">— skip —</option>
{{range $.Pos}}<option value="{{.ID}}">{{.Name}} ({{.Currency}})</option>{{end}}
</select>
<input name="amount_{{$i}}" type="text" inputmode="numeric" pattern="[0-9.,]*" placeholder="e.g. 12.000.000"
  aria-label="Amount for line {{$i}}">
</div>
{{end}}
{{end}}
</div>
<p style="margin: 8px 0 0;"><a href="#" class="linkbtn" onclick="return addLine()">+ Add line</a></p>
<button type="submit" style="margin-top: 16px;">Create template</button>
</form>
<script>
function addLine(){var c=document.getElementById("alloc-lines"),n=c.children.length,
lc=document.getElementById("line-count"),r=c.lastElementChild.cloneNode(true),
s=r.querySelector("select"),inp=r.querySelector("input");
s.name="pos_id_"+n;s.selectedIndex=0;inp.name="amount_"+n;inp.value="";
c.appendChild(r);lc.value=n+1;return false}
</script>
<p class="aside"><a class="linkbtn" href="/income-templates">&larr; Back</a></p>`

const incomeTemplateDetailBody = `<header class="page-head">
  <div>
    <p class="home-eyebrow">Income template</p>
    <h1>{{.Name}}</h1>
  </div>
</header>
<p class="subtitle">Allocation total {{money .LinesTotal "idr"}}{{if .HasLeftoverPos}} &middot; leftover → <strong>{{.LeftoverPosName}}</strong>{{end}}</p>
{{if .Flash}}<div class="alert" role="status">{{.Flash}}</div>{{end}}

<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Edit template</span>
  </div>
  <div style="padding: 16px;">
  <form method="post" action="/income-templates/{{.ID}}/edit" id="edit-form">
  <input type="hidden" name="line_count" id="edit-line-count" value="{{if .Lines}}{{len .Lines}}{{else}}1{{end}}">
  <div class="field">
  <label for="edit-name">Name</label>
  <input id="edit-name" name="name" type="text" value="{{.Name}}" required maxlength="80">
  </div>
  <div class="field">
  <label for="edit-leftover">Leftover Pos <span style="color:var(--text-tertiary); font-weight:400;">(optional)</span></label>
  <select id="edit-leftover" name="leftover_pos_id">
  <option value="">— none —</option>
  {{range .Pos}}<option value="{{.ID}}"{{if eq .ID $.LeftoverPosID}} selected{{end}}>{{.Name}} ({{.Currency}})</option>{{end}}
  </select>
  </div>
  <h3 style="font-size: var(--font-sm); font-weight: 600; margin: 16px 0 8px;">Lines</h3>
  <div class="alloc" id="edit-alloc-lines">
  {{if .Lines}}
  {{range $i, $line := .Lines}}
  <div class="alloc-row">
  <select name="pos_id_{{$i}}">
  <option value="">— remove —</option>
  {{range $.Pos}}<option value="{{.ID}}"{{if eq .ID $line.PosID}} selected{{end}}>{{.Name}} ({{.Currency}})</option>{{end}}
  </select>
  <input name="amount_{{$i}}" type="text" inputmode="numeric" pattern="[0-9.,]*" value="{{fmtAmt $line.Amount}}"
    placeholder="e.g. 12.000.000">
  </div>
  {{end}}
  {{else}}
  <div class="alloc-row">
  <select name="pos_id_0">
  <option value="">— skip —</option>
  {{range $.Pos}}<option value="{{.ID}}">{{.Name}} ({{.Currency}})</option>{{end}}
  </select>
  <input name="amount_0" type="text" inputmode="numeric" pattern="[0-9.,]*" placeholder="e.g. 12.000.000">
  </div>
  {{end}}
  </div>
  <p style="margin: 8px 0 0;"><a href="#" class="linkbtn" onclick="return addEditLine()">+ Add line</a></p>
  <button type="submit" style="margin-top: 16px;">Save changes</button>
  </form>
  </div>
</section>

<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Apply &rarr; preview allocation</span>
  </div>
  <div style="padding: 16px;">
  <p class="subtitle" style="margin: 0 0 12px;">Enter the details; the next step shows the suggested split — you can adjust it before approving.</p>
  <form method="post" action="/income-templates/{{.ID}}/preview">
  <div class="field">
  <label for="amount">Incoming amount (IDR)</label>
  <input id="amount" name="amount" type="text" inputmode="numeric" pattern="[0-9.,]*" required
    placeholder="e.g. 25.000.000">
  <p class="hint">Suggested allocation total: {{money .LinesTotal "idr"}}{{if .HasLeftoverPos}} (overflow → {{.LeftoverPosName}}){{end}}.</p>
  </div>
  <div class="field">
  <label for="effective_date">Effective date</label>
  <input id="effective_date" name="effective_date" type="date" required>
  </div>
  <div class="field">
  <label for="counterparty_name">Counterparty</label>
  <input id="counterparty_name" name="counterparty_name" type="text" required maxlength="80"
    placeholder="e.g. PT Telkom">
  </div>
  <button type="submit">Preview allocation</button>
  </form>
  </div>
</section>
<script>
function addEditLine(){var c=document.getElementById("edit-alloc-lines"),n=c.children.length,
lc=document.getElementById("edit-line-count"),r=c.lastElementChild.cloneNode(true),
s=r.querySelector("select"),inp=r.querySelector("input");
s.name="pos_id_"+n;s.selectedIndex=0;inp.name="amount_"+n;inp.value="";
c.appendChild(r);lc.value=n+1;return false}
</script>
<p class="aside"><a class="linkbtn" href="/income-templates">&larr; All templates</a></p>`

const incomeTemplatePreviewBody = `<h1>Review allocation</h1>
<p class="subtitle">Template &middot; <strong>{{.TemplateName}}</strong></p>

<section class="card">
<h2>Incoming</h2>
<table>
<tbody>
<tr><td>Amount</td><td class="num"><strong>{{money .Amount "idr"}}</strong></td></tr>
<tr><td>Date</td><td class="num">{{.EffectiveDate}}</td></tr>
<tr><td>Counterparty</td><td class="num">{{.CounterpartyName}}</td></tr>
</tbody>
</table>
</section>

{{if .SuggestionNotice}}
<div class="alert" role="alert">{{.SuggestionNotice}}</div>
{{end}}

<form method="post" action="/income-templates/{{.ID}}/apply">
<input type="hidden" name="amount" value="{{.AmountRaw}}">
<input type="hidden" name="effective_date" value="{{.EffectiveDate}}">
<input type="hidden" name="counterparty_name" value="{{.CounterpartyName}}">
<input type="hidden" name="idempotency_key" value="{{.IdempotencyKey}}">

<section class="card">
<h2>Allocation</h2>
<p class="subtitle">Adjust as needed. The rows must sum to {{money .Amount "idr"}}.</p>
<div class="alloc">
{{range $i, $row := .Rows}}
<div class="alloc-row">
<select name="alloc_pos_{{$i}}" aria-label="Pos for row {{$i}}">
<option value="">— skip —</option>
{{range $.PosOptions}}<option value="{{.ID}}"{{if eq .ID $row.PosID}} selected{{end}}>{{.Name}} ({{.Currency}})</option>{{end}}
</select>
<input name="alloc_amount_{{$i}}" type="text" inputmode="numeric" pattern="[0-9.,]*"
  value="{{$row.Amount}}" placeholder="e.g. 12.000.000" aria-label="Amount for row {{$i}}">
</div>
{{end}}
<div class="alloc-total"><span>Salary total to allocate</span><strong>{{money .Amount "idr"}}</strong></div>
</div>
<p class="hint">Tip: leave a row empty to drop it. Add a Pos to a previously-empty row to introduce a new line. Each Pos can appear only once.</p>
</section>

<button type="submit">Approve and apply</button>
</form>
<p class="aside"><a class="linkbtn" href="/income-templates/{{.ID}}">&larr; Back to template</a></p>`

// SettingsData drives /settings — currently just the theme switch.
type SettingsData struct {
	Title        string
	DisplayName  string
	UnreadCount  int
	CurrentTheme string // "light" | "dark" | "auto"
}

func (d SettingsData) SignedIn() bool { return d.DisplayName != "" }
func (d SettingsData) Compact() bool  { return false }
func (d SettingsData) Wide() bool     { return false }
func (d SettingsData) HideBell() bool { return false }
func (d SettingsData) Route() string  { return "settings" }

// AccountManageRow is a row on /accounts. Drives the rename + archive
// forms inline; archived rows render with the action area suppressed.
type AccountManageRow struct {
	ID       string
	Name     string
	Archived bool
}

// AccountsData drives /accounts (manage list view).
type AccountsData struct {
	Title       string
	DisplayName string
	UnreadCount int
	Accounts    []AccountManageRow
	Flash       string
	Error       string
}

func (d AccountsData) SignedIn() bool { return d.DisplayName != "" }
func (d AccountsData) Compact() bool  { return false }
func (d AccountsData) Wide() bool     { return false }
func (d AccountsData) HideBell() bool { return false }
func (d AccountsData) Route() string  { return "accounts" }

// AccountNewData drives /accounts/new — name round-trips on validation.
type AccountNewData struct {
	Title       string
	DisplayName string
	UnreadCount int
	Name        string
	Errors      []string
}

func (d AccountNewData) SignedIn() bool { return d.DisplayName != "" }
func (d AccountNewData) Compact() bool  { return true }
func (d AccountNewData) Wide() bool     { return false }
func (d AccountNewData) HideBell() bool { return false }
func (d AccountNewData) Route() string  { return "accounts" }

const accountsBody = `<header class="page-head">
  <div>
    <p class="home-eyebrow">Money &middot; purses</p>
    <h1>Accounts</h1>
  </div>
  <div class="page-head-actions">
    <a class="pill primary" href="/accounts/new">+ New Account</a>
  </div>
</header>

{{if .Flash}}<p class="success" role="status">{{.Flash}}</p>{{end}}
{{if .Error}}<p class="alert" role="alert">{{.Error}}</p>{{end}}
{{if not .Accounts}}
<div class="section-card">
<div class="empty-state">
<p class="empty-state-text">No accounts yet.</p>
<p class="empty-state-hint">Create one to start tracking money.</p>
</div>
</div>
{{else}}
<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">All Accounts</span>
    <span class="section-card-meta">{{len .Accounts}} items</span>
  </div>
  <div class="table-wrap">
  <table>
  <thead><tr><th>Name</th><th>Status</th><th>Actions</th></tr></thead>
  <tbody>
  {{range .Accounts}}
  <tr>
  <td>
    <form method="post" action="/accounts/{{.ID}}/rename" style="display:flex; gap:8px; align-items:center; min-width:280px;">
      <input type="text" name="name" value="{{.Name}}" required maxlength="80" style="flex:1 1 auto; min-width:0; width:100%;">
      <button type="submit" style="white-space:nowrap;">Rename</button>
    </form>
  </td>
  <td>{{if .Archived}}<span class="chip">archived</span>{{else}}<span class="chip chip-in">active</span>{{end}}</td>
  <td><div style="display:flex; gap:6px; flex-wrap:wrap;">
  {{if not .Archived}}
    <form method="post" action="/accounts/{{.ID}}/archive" onsubmit="return confirm('Archive this account? Existing Pos still pointing at it will keep working but the account will be hidden from default lists.');" style="display:inline;">
      <button type="submit">Archive</button>
    </form>
  {{end}}
    <form method="post" action="/accounts/{{.ID}}/delete" onsubmit="return confirm('Delete this account permanently? Only succeeds if no Pos points at it.');" style="display:inline;">
      <button type="submit">Delete</button>
    </form>
  </div></td>
  </tr>
  {{end}}
  </tbody>
  </table>
  </div>
</section>
{{end}}`

const accountNewBody = `<h1>New Account</h1>
<p class="subtitle">Create an IDR purse. After creation, point a Pos at it via the Pos detail page.</p>
{{if .Errors}}
<div class="alert" role="alert">
<strong>Couldn&rsquo;t save this Account:</strong>
<ul style="margin:8px 0 0 20px; padding:0;">
{{range .Errors}}<li>{{.}}</li>{{end}}
</ul>
</div>
{{end}}
<form method="post" action="/accounts">
<div class="field">
<label for="name">Name</label>
<input id="name" name="name" type="text" value="{{.Name}}"
  autocapitalize="words" autocorrect="off" spellcheck="false"
  required maxlength="80"
  placeholder="e.g. BCA Joint, Cash, Mandiri Riza">
</div>
<button type="submit">Create Account</button>
</form>
<p class="aside"><a class="linkbtn" href="/accounts">&larr; Cancel</a></p>`

const settingsBody = `<header class="page-head">
  <div>
    <p class="home-eyebrow">Preferences</p>
    <h1>Settings</h1>
  </div>
</header>

<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Theme</span>
    <span class="section-card-meta">Currently: {{.CurrentTheme}}</span>
  </div>
  <div style="padding: 16px;">
    <p class="subtitle" style="margin: 0 0 12px;">Choose how the app looks on this device. <strong>Auto</strong> follows your system preference.</p>
    <form method="post" action="/settings/theme" class="theme-switch">
    <button type="submit" name="theme" value="light"{{if eq .CurrentTheme "light"}} class="active"{{end}}>☀ Light</button>
    <button type="submit" name="theme" value="dark"{{if eq .CurrentTheme "dark"}} class="active"{{end}}>☾ Dark</button>
    <button type="submit" name="theme" value="auto"{{if eq .CurrentTheme "auto"}} class="active"{{end}}>◐ Auto (follow OS)</button>
    </form>
  </div>
</section>`
