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
	template.Must(t.New("pos").Parse(layoutOpen + posBody + layoutClose))
	template.Must(t.New("pos_new").Parse(layoutOpen + posNewBody + layoutClose))
	template.Must(t.New("spending").Parse(layoutOpen + spendingBody + layoutClose))
	template.Must(t.New("income_templates").Parse(layoutOpen + incomeTemplatesListBody + layoutClose))
	template.Must(t.New("income_template_new").Parse(layoutOpen + incomeTemplateNewBody + layoutClose))
	template.Must(t.New("income_template_detail").Parse(layoutOpen + incomeTemplateDetailBody + layoutClose))
	template.Must(t.New("income_template_preview").Parse(layoutOpen + incomeTemplatePreviewBody + layoutClose))
	template.Must(t.New("settings").Parse(layoutOpen + settingsBody + layoutClose))
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
func (d LoginData) Wide() bool     { return false }

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
	Accounts      []AccountRow
	PosByCurrency []PosCurrencyGroup
	LoadError     bool
	UnreadCount   int // server-rendered bell badge (no JS, full-page poll)
}

// SignedIn for HomeData mirrors NotificationsData — the home page is only
// reachable post-auth, so a populated DisplayName is the trigger.
func (d HomeData) SignedIn() bool  { return d.DisplayName != "" }
func (d HomeData) Compact() bool   { return false }
func (d HomeData) Wide() bool      { return false }
func (d HomeData) HideBell() bool  { return false }
func (d HomeData) Route() string   { return "home" }

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

// PosNewData drives the "create Pos" form. Name/Currency/TargetRaw
// round-trip on validation failure so the user doesn't retype.
type PosNewData struct {
	Title       string
	DisplayName string
	UnreadCount int
	Name        string
	Currency    string
	TargetRaw   string   // string form so empty stays empty across re-renders
	Errors      []string // list of validation messages, all rendered together
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
	Items       []TransactionRow
	LoadError   bool
	UnreadCount int
}

// SignedIn for transactions list — only reachable post-auth.
func (d TransactionsData) SignedIn() bool { return d.DisplayName != "" }
func (d TransactionsData) Compact() bool  { return false }
func (d TransactionsData) Wide() bool     { return true }
func (d TransactionsData) HideBell() bool { return false }
func (d TransactionsData) Route() string  { return "transactions" }

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
	Name        string
	BalanceIDR  int64 // smallest unit (rupiah cents); 0 when balance computation isn't wired
}

// PosCurrencyGroup groups Pos rows by their currency for §6.2 rendering.
type PosCurrencyGroup struct {
	Currency string
	Items    []PosRow
}

// PosRow is one row in a per-currency Pos table.
type PosRow struct {
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
body {
  background: var(--bg-page);
  color: var(--text);
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto,
               "Helvetica Neue", Arial, "PingFang SC", "Hiragino Sans GB",
               "Microsoft YaHei", sans-serif;
  font-size: var(--font-base); line-height: 1.5714;
  min-height: 100vh; display: grid;
  align-items: start; justify-items: center;
  padding: 24px;
}
@media (max-width: 360px) { body { padding: 16px; } }
main {
  position: relative; /* anchor for the bell */
  width: 100%; max-width: 720px;
  background: var(--bg-container);
  border-radius: var(--radius-lg);
  padding: 32px;
  box-shadow: var(--shadow-sm);
  border: 1px solid var(--border-secondary);
}
main.compact { max-width: 420px; padding: 32px 28px; }
main.wide    { max-width: 920px; }
@media (max-width: 480px) { main { padding: 20px; border-radius: 0;
  border-left: 0; border-right: 0; } }
h1 { font-size: var(--font-h2); font-weight: 600; line-height: 1.21;
  margin: 0 0 16px; color: var(--text); }
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

/* AntD Table */
table {
  width: 100%; border-collapse: collapse;
  font-size: var(--font-base); color: var(--text);
}
thead th {
  background: var(--bg-fill); color: var(--text);
  font-weight: 500; padding: 12px 16px;
  border-bottom: 1px solid var(--border-secondary); text-align: left;
}
tbody td {
  padding: 12px 16px;
  border-bottom: 1px solid var(--border-secondary);
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

/* Notifications feed */
.notifs { list-style: none; margin: 0; padding: 0; }
.notif {
  display: flex; gap: 12px; padding: 12px 0;
  border-bottom: 1px solid var(--border-secondary);
}
.notif:last-child { border-bottom: 0; }
.notif.unread .notif-link strong { color: var(--text); font-weight: 600; }
.notif:not(.unread) .notif-link strong { color: var(--text-secondary); font-weight: 400; }
.notif-link { flex: 1; display: block; text-decoration: none; color: inherit; }
.notif-body { display: block; font-size: var(--font-base); color: var(--text-secondary); margin-top: 4px; }
.notif-time { display: block; font-size: var(--font-sm); color: var(--text-tertiary); margin-top: 4px; }
.notif-actions { flex-shrink: 0; }

/* Filter row — input + button both AntD middle-size (32px tall). */
.filter { display: flex; gap: 12px; align-items: end; margin: 0 0 24px; flex-wrap: wrap; }
.filter label { display: flex; flex-direction: column; gap: 4px;
  font-size: var(--font-sm); color: var(--text-tertiary); }
.filter input { width: auto; min-width: 144px; height: 32px; padding: 4px 11px; }
.filter button { width: auto; height: 32px; padding: 0 16px; box-shadow: 0 2px 0 rgba(35, 120, 4, 0.12); }

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
 * income green (`+`), expense default (chip carries red), transfers muted. */
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

.nav {
  display: flex; gap: 24px; align-items: baseline; margin: 0 0 24px;
  font-size: var(--font-base);
  padding-bottom: 16px;
  border-bottom: 1px solid var(--border-secondary);
}
.nav a {
  color: var(--text-secondary); text-decoration: none;
  padding-bottom: 16px; margin-bottom: -17px;
  border-bottom: 2px solid transparent;
  transition: color 0.2s, border-color 0.2s;
}
.nav a:hover { color: var(--primary); }
.nav a[aria-current="page"] {
  color: var(--primary); font-weight: 500;
  border-bottom-color: var(--primary);
}
.nav-end { margin-left: auto; }
.nav-end .linkbtn { color: var(--text-tertiary); }
.nav-end .linkbtn:hover { color: var(--primary); }

/* Theme switcher — three side-by-side buttons; the active one
 * adopts the primary fill so the user sees their current pick. */
.theme-switch {
  display: flex; gap: 12px; margin: 0 0 12px; flex-wrap: wrap;
}
.theme-switch button {
  width: auto; padding: 6px 14px;
  background: var(--bg-container); color: var(--text);
  border: 1px solid var(--border);
  box-shadow: none;
  font-weight: 400;
}
.theme-switch button:hover:not(.active):not(:disabled) {
  border-color: var(--primary); color: var(--primary); background: var(--bg-container);
}
.theme-switch button.active {
  background: var(--primary); color: #fff;
  border-color: var(--primary);
  font-weight: 500;
}
</style>
</head>
<body>
<main{{if .Compact}} class="compact"{{else if .Wide}} class="wide"{{end}}>
{{if .SignedIn}}
<nav class="nav" aria-label="Primary">
<a href="/"{{if eq .Route "home"}} aria-current="page"{{end}}>Home</a>
<a href="/transactions"{{if eq .Route "transactions"}} aria-current="page"{{end}}>Transactions</a>
<a href="/spending"{{if eq .Route "spending"}} aria-current="page"{{end}}>Spending</a>
<a href="/income-templates"{{if eq .Route "income"}} aria-current="page"{{end}}>Income</a>
<a href="/notifications"{{if eq .Route "notifications"}} aria-current="page"{{end}}>Notifications<span class="badge" aria-label="{{.UnreadCount}} unread">{{if .UnreadCount}}{{.UnreadCount}}{{end}}</span></a>
<a href="/settings" class="nav-end"{{if eq .Route "settings"}} aria-current="page"{{end}} aria-label="Settings">⚙</a>
<form method="post" action="/logout">
<button type="submit" class="linkbtn">Sign out</button>
</form>
</nav>
{{end}}
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

const notificationsBody = `<h1>Notifications</h1>
{{if .LoadError}}
<p class="alert" role="alert">Couldn&rsquo;t load notifications. Refresh in a moment.</p>
{{else if not .Items}}
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
{{else}}
{{if .UnreadCount}}
<form method="post" action="/notifications/mark-all-read" class="aside">
<button type="submit" class="linkbtn">Mark all read ({{.UnreadCount}})</button>
</form>
{{end}}
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
{{end}}`

const spendingBody = `<h1>Spending</h1>
<form method="get" action="/spending" class="filter">
<label>From <input type="date" name="from" value="{{.From}}"></label>
<label>To <input type="date" name="to" value="{{.To}}"></label>
<button type="submit">Filter</button>
</form>
{{if .LoadError}}
<p class="alert" role="alert">Couldn&rsquo;t load spending. Refresh in a moment.</p>
{{else if not .Columns}}
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
{{else}}
<p class="subtitle">Top {{.TopN}} Pos by spending in this range.</p>
<div class="table-wrap">
<table>
<thead>
<tr>
<th>Month</th>
{{range .Columns}}<th class="num"><a href="/pos/{{.PosID}}">{{.Name}}</a></th>{{end}}
<th class="num">Row total</th>
</tr>
</thead>
<tbody>
{{range $row := .Rows}}
<tr>
<td>{{$row.Month}}</td>
{{range $i, $c := $row.Cells}}<td class="num">{{if $c}}{{money $c (index $.Columns $i).Currency}}{{else}}&mdash;{{end}}</td>{{end}}
<td class="num">{{if $.MixedCurrency}}&mdash;{{else}}<strong>{{money $row.Total (index $.Columns 0).Currency}}</strong>{{end}}</td>
</tr>
{{end}}
<tr class="totals">
<td><strong>Pos total</strong></td>
{{range .Columns}}<td class="num"><strong>{{money .Total .Currency}}</strong></td>{{end}}
<td class="num">&mdash;</td>
</tr>
</tbody>
</table>
</div>
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

{{if .Obligations}}
<section class="card">
<h2>Open obligations</h2>
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
<label for="target">Target <span style="color:var(--text-tertiary); font-weight:400;">(optional)</span></label>
<input id="target" name="target" type="text" inputmode="numeric"
  value="{{.TargetRaw}}"
  autocapitalize="off" autocorrect="off" spellcheck="false"
  pattern="[0-9]*" maxlength="16"
  placeholder="e.g. 12000000 for Rp 12.000.000">
<p class="hint">Whole number in the smallest unit (rupiah for IDR, cents for USD). Leave blank for an open-ended Pos.</p>
</div>
<button type="submit">Create Pos</button>
</form>
<p class="aside"><a class="linkbtn" href="/">&larr; Cancel</a></p>`

const transactionsBody = `<h1>Transactions</h1>
<form method="get" action="/transactions" class="filter">
<label>From <input type="date" name="from" value="{{.From}}"></label>
<label>To <input type="date" name="to" value="{{.To}}"></label>
<button type="submit">Filter</button>
</form>
{{if .LoadError}}
<p class="alert" role="alert">Couldn&rsquo;t load transactions. Refresh in a moment.</p>
{{else if not .Items}}
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
{{else}}
<div class="table-wrap">
<table>
<thead><tr>
<th>Date</th><th>Type</th><th class="num">Amount</th>
<th>Account</th><th>Pos</th><th>Counterparty</th><th>Note</th>
</tr></thead>
<tbody>
{{range .Items}}
<tr{{if .IsReversal}} class="reversal"{{end}}>
<td>{{.EffectiveDate}}</td>
<td><span class="chip {{txnChip .Type}}">{{txnLabel .Type}}</span>{{if .IsReversal}} <a class="badge-rev" href="/transactions/{{.ReversesID}}">reverses</a>{{end}}</td>
<td class="num {{txnAmt .Type}}">{{txnSign .Type}}{{money .Amount .Currency}}</td>
<td>{{if .AccountName}}{{.AccountName}}{{else}}&mdash;{{end}}</td>
<td>{{if .PosName}}{{.PosName}}{{else}}&mdash;{{end}}</td>
<td>{{if .CounterpartyName}}{{.CounterpartyName}}{{else}}&mdash;{{end}}</td>
<td>{{.Note}}</td>
</tr>
{{end}}
</tbody>
</table>
</div>
{{end}}`

const homeBody = `<h1>Hi, {{.DisplayName}}</h1>
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

{{if .Accounts}}
<section class="card">
<h2>Accounts</h2>
<table>
<thead><tr><th>Name</th><th class="num">Balance</th></tr></thead>
<tbody>
{{range .Accounts}}
<tr><td>{{.Name}}</td><td class="num{{if lt .BalanceIDR 0}} neg-cash{{end}}">{{money .BalanceIDR "IDR"}}</td></tr>
{{end}}
</tbody>
</table>
</section>
{{end}}

{{if .PosByCurrency}}<p class="aside" style="text-align:right; margin: 0 0 -8px;"><a class="linkbtn" href="/pos/new">+ New Pos</a></p>{{end}}
{{range $g := .PosByCurrency}}
<section class="card">
<h2>Pos &mdash; {{$g.Currency}}</h2>
<table>
<thead><tr><th>Name</th><th class="num">Cash</th><th class="num">Target</th></tr></thead>
<tbody>
{{range $g.Items}}
<tr>
  <td>{{.Name}}</td>
  <td class="num{{if lt .Cash 0}} neg-cash{{end}}">{{money .Cash $g.Currency}}</td>
  <td class="num">{{if .HasTarget}}{{money .Target $g.Currency}}<span class="progress" aria-label="{{pct .Cash .Target}}% of target"><span class="progress-fill" style="width: {{pct .Cash .Target}}%"></span></span>{{else}}&mdash;{{end}}</td>
</tr>
{{end}}
</tbody>
</table>
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

const incomeTemplatesListBody = `<h1>Income templates</h1>
<p class="subtitle">Each template names an income type and the fixed allocation across Pos when it lands.</p>
<p class="aside" style="text-align:right; margin: 0 0 -8px;"><a class="linkbtn" href="/income-templates/new">+ New template</a></p>
{{if .LoadError}}
<p class="alert" role="alert">Couldn&rsquo;t load templates. Refresh in a moment.</p>
{{else if not .Items}}
<div class="empty-state">
<p class="empty-state-text">No income templates yet.</p>
<p class="empty-state-hint">Create one to fan-out a salary across Pos in one step.</p>
</div>
{{else}}
<table>
<thead><tr><th>Name</th><th class="num">Lines total</th></tr></thead>
<tbody>
{{range .Items}}
<tr><td><a href="/income-templates/{{.ID}}">{{.Name}}</a></td><td class="num">{{money .Total "idr"}}</td></tr>
{{end}}
</tbody>
</table>
{{end}}`

const incomeTemplateNewBody = `<h1>New income template</h1>
<p class="subtitle">Each line allocates a fixed amount to one Pos. Up to 8 lines.</p>
{{if .Errors}}
<div class="alert" role="alert">
<strong>Couldn&rsquo;t save template:</strong>
<ul style="margin:8px 0 0 20px; padding:0;">
{{range .Errors}}<li>{{.}}</li>{{end}}
</ul>
</div>
{{end}}
<form method="post" action="/income-templates">
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
<table>
<thead><tr><th>Pos</th><th class="num">Amount (smallest unit)</th></tr></thead>
<tbody>
{{range $i, $line := .Lines}}
<tr>
<td><select name="pos_id_{{$i}}">
<option value="">— skip —</option>
{{range $.Pos}}<option value="{{.ID}}"{{if eq .ID $line.PosID}} selected{{end}}>{{.Name}} ({{.Currency}})</option>{{end}}
</select></td>
<td><input name="amount_{{$i}}" type="text" inputmode="numeric" pattern="[0-9]*" value="{{$line.Amount}}"
  placeholder="e.g. 12000000"></td>
</tr>
{{end}}
{{if not .Lines}}
{{range $i := (intRange 0 8)}}
<tr>
<td><select name="pos_id_{{$i}}">
<option value="">— skip —</option>
{{range $.Pos}}<option value="{{.ID}}">{{.Name}} ({{.Currency}})</option>{{end}}
</select></td>
<td><input name="amount_{{$i}}" type="text" inputmode="numeric" pattern="[0-9]*" placeholder="e.g. 12000000"></td>
</tr>
{{end}}
{{end}}
</tbody>
</table>
<button type="submit" style="margin-top: 16px;">Create template</button>
</form>
<p class="aside"><a class="linkbtn" href="/income-templates">&larr; Back</a></p>`

const incomeTemplateDetailBody = `<h1>{{.Name}}</h1>
<p class="subtitle">Income template &middot; allocation total {{money .LinesTotal "idr"}}{{if .HasLeftoverPos}} &middot; leftover → <strong>{{.LeftoverPosName}}</strong>{{end}}</p>
{{if .Flash}}<div class="alert" role="status">{{.Flash}}</div>{{end}}

<section class="card">
<h2>Lines</h2>
{{if .Lines}}
<table>
<thead><tr><th>Pos</th><th class="num">Amount</th></tr></thead>
<tbody>
{{range .Lines}}
<tr><td>{{.PosName}}</td><td class="num">{{money .Amount .PosCurrency}}</td></tr>
{{end}}
<tr class="totals"><td><strong>Total</strong></td><td class="num"><strong>{{money .LinesTotal "idr"}}</strong></td></tr>
</tbody>
</table>
{{else}}
<p class="subtitle">No lines yet.</p>
{{end}}
</section>

<section class="card">
<h2>New incoming → preview allocation</h2>
<p class="subtitle">Enter the details; the next step shows the suggested split (from this template) — you can adjust it before approving.</p>
<form method="post" action="/income-templates/{{.ID}}/preview">
<div class="field">
<label for="amount">Incoming amount (IDR)</label>
<input id="amount" name="amount" type="text" inputmode="numeric" pattern="[0-9]*" required
  placeholder="e.g. 25000000">
<p class="hint">Suggested allocation total: {{money .LinesTotal "idr"}}{{if .HasLeftoverPos}} (overflow → {{.LeftoverPosName}}){{end}}. You can override the split on the next page.</p>
</div>
<div class="field">
<label for="effective_date">Effective date</label>
<input id="effective_date" name="effective_date" type="date" required>
</div>
<div class="field">
<label for="account_id">Receiving account</label>
<select id="account_id" name="account_id" required>
<option value="">— select —</option>
{{range .Accounts}}<option value="{{.ID}}">{{.Name}}</option>{{end}}
</select>
</div>
<div class="field">
<label for="counterparty_name">Counterparty</label>
<input id="counterparty_name" name="counterparty_name" type="text" required maxlength="80"
  placeholder="e.g. PT Telkom">
</div>
<button type="submit">Preview allocation</button>
</form>
</section>

<p class="aside"><a class="linkbtn" href="/income-templates">&larr; All templates</a></p>`

const incomeTemplatePreviewBody = `<h1>Review allocation</h1>
<p class="subtitle">Template &middot; <strong>{{.TemplateName}}</strong></p>

<section class="card">
<h2>Incoming</h2>
<table>
<tbody>
<tr><td>Amount</td><td class="num"><strong>{{money .Amount "idr"}}</strong></td></tr>
<tr><td>Date</td><td class="num">{{.EffectiveDate}}</td></tr>
<tr><td>Account</td><td class="num">{{.AccountName}}</td></tr>
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
<input type="hidden" name="account_id" value="{{.AccountID}}">
<input type="hidden" name="counterparty_name" value="{{.CounterpartyName}}">
<input type="hidden" name="idempotency_key" value="{{.IdempotencyKey}}">

<section class="card">
<h2>Allocation</h2>
<p class="subtitle">Adjust as needed. The rows must sum to {{money .Amount "idr"}}.</p>
<table>
<thead><tr><th>Pos</th><th class="num">Amount (IDR, smallest unit)</th></tr></thead>
<tbody>
{{range $i, $row := .Rows}}
<tr>
<td><select name="alloc_pos_{{$i}}">
<option value="">— skip —</option>
{{range $.PosOptions}}<option value="{{.ID}}"{{if eq .ID $row.PosID}} selected{{end}}>{{.Name}} ({{.Currency}})</option>{{end}}
</select></td>
<td><input name="alloc_amount_{{$i}}" type="text" inputmode="numeric" pattern="[0-9]*"
  value="{{$row.Amount}}" placeholder="e.g. 12000000"></td>
</tr>
{{end}}
<tr class="totals"><td><strong>Salary total to allocate</strong></td><td class="num"><strong>{{money .Amount "idr"}}</strong></td></tr>
</tbody>
</table>
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

const settingsBody = `<h1>Settings</h1>
<p class="subtitle">Display preferences for this device. Stored in a cookie.</p>

<section class="card">
<h2>Theme</h2>
<p class="subtitle">Choose how the app looks on this device. <strong>Auto</strong> follows your system preference.</p>
<form method="post" action="/settings/theme" class="theme-switch">
<button type="submit" name="theme" value="light"{{if eq .CurrentTheme "light"}} class="active"{{end}}>☀ Light</button>
<button type="submit" name="theme" value="dark"{{if eq .CurrentTheme "dark"}} class="active"{{end}}>☾ Dark</button>
<button type="submit" name="theme" value="auto"{{if eq .CurrentTheme "auto"}} class="active"{{end}}>◐ Auto (follow OS)</button>
</form>
<p class="hint">Currently selected: <strong>{{.CurrentTheme}}</strong>.</p>
</section>`
