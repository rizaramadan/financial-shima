// Package template owns the html/template definitions for the web layer.
// Each page is a complete document parsed into its own template — there
// is no shared "body" block (which would collide across pages in a single
// template set). Layout chrome is shared via Go string concatenation,
// keeping all template strings as Go consts (no filesystem dependency).
package template

import (
	"html/template"
	"io"
	"time"

	"github.com/labstack/echo/v4"
)

// Renderer satisfies echo.Renderer using parsed html/templates.
type Renderer struct {
	t *template.Template
}

func New() *Renderer {
	t := template.New("").Funcs(template.FuncMap{
		"relTime": relativeTime,
	})
	template.Must(t.New("login").Parse(layoutOpen + loginBody + layoutClose))
	template.Must(t.New("verify").Parse(layoutOpen + verifyBody + layoutClose))
	template.Must(t.New("home").Parse(layoutOpen + homeBody + layoutClose))
	template.Must(t.New("notifications").Parse(layoutOpen + notificationsBody + layoutClose))
	template.Must(t.New("transactions").Parse(layoutOpen + transactionsBody + layoutClose))
	template.Must(t.New("pos").Parse(layoutOpen + posBody + layoutClose))
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

func (r *Renderer) Render(w io.Writer, name string, data interface{}, _ echo.Context) error {
	return r.t.ExecuteTemplate(w, name, data)
}

// LoginData drives the login template. Error is non-empty when the user
// just submitted an unknown identifier or hit cooldown.
type LoginData struct {
	Title string
	Error string
}

// VerifyData drives the OTP-entry template. Identifier round-trips so the
// hidden field can replay it on POST.
type VerifyData struct {
	Title      string
	Identifier string
	Error      string
}

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
func (d HomeData) SignedIn() bool { return d.DisplayName != "" }

// LoginData and VerifyData are pre-auth; SignedIn always false.
func (d LoginData) SignedIn() bool  { return false }
func (d VerifyData) SignedIn() bool { return false }

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
	Receivables  int64
	Payables     int64
	Obligations  []ObligationRow
	Transactions []PosTransactionRow
	NotFound     bool
	LoadError    bool
}

// SignedIn — only authenticated users reach pos detail.
func (d PosDetailData) SignedIn() bool { return d.DisplayName != "" }

// ObligationRow is one open obligation involving this Pos. Direction is
// "receivable" (this pos is creditor) or "payable" (this pos is debtor).
type ObligationRow struct {
	ID          string
	Direction   string // "receivable" | "payable"
	OtherPosID  string
	Currency    string
	Outstanding int64
	CreatedAt   time.Time
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
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>Shima &mdash; {{.Title}}</title>
<style>
:root {
  --bg: #fafaf9; --fg: #1c1917; --muted: #57534e; --border: #d6d3d1;
  --accent: #0f172a; --accent-fg: #f8fafc; --focus: #2563eb; --error: #b91c1c;
  --radius: 0.5rem;
  accent-color: var(--focus);
}
::selection { background: color-mix(in oklab, var(--focus) 25%, transparent); }
@media (prefers-color-scheme: dark) {
  :root { --bg: #0c0a09; --fg: #f5f5f4; --muted: #a8a29e; --border: #44403c;
    --accent: #d6d3d1; --accent-fg: #0c0a09; --focus: #93c5fd; --error: #fca5a5; }
}
* { box-sizing: border-box; }
html, body { margin: 0; padding: 0; }
body {
  background: var(--bg); color: var(--fg);
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto,
               "Helvetica Neue", Arial, sans-serif;
  font-size: 16px; line-height: 1.5;
  min-height: 100vh; display: grid;
  align-items: start; justify-items: center;
  padding: max(1.5rem, 12vh) 1.5rem 1.5rem;
}
@media (max-width: 360px) { body { padding: 1rem; } }
main { width: 100%; max-width: 24rem; }
h1 { font-size: 1.875rem; font-weight: 600; margin: 0 0 1.5rem; letter-spacing: -0.02em; }
form { margin: 0; }
.field { margin-bottom: 1.5rem; }
label { display: block; font-size: 0.875rem; font-weight: 500; margin-bottom: 0.5rem; }
.hint { display: block; font-size: 0.8125rem; color: var(--muted); margin: 0.5rem 0 0; }
input { width: 100%; padding: 0.625rem 0.75rem; font: inherit; font-size: max(1rem, 16px);
  color: var(--fg); background: var(--bg); border: 1px solid var(--border);
  border-radius: var(--radius); }
input:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
button { width: 100%; padding: 0.875rem 1rem; font: inherit; font-size: max(1rem, 16px); font-weight: 600;
  color: var(--accent-fg); background: var(--accent); border: 1px solid var(--accent);
  border-radius: var(--radius); cursor: pointer; }
button:hover:not(:disabled) {
  background: color-mix(in oklab, var(--accent) 85%, var(--fg));
  border-color: color-mix(in oklab, var(--accent) 85%, var(--fg));
}
button:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
button:disabled { background: var(--border); color: color-mix(in oklab, var(--fg) 60%, transparent);
  border-color: var(--border); cursor: not-allowed; }
.alert { margin: 0 0 1rem; padding: 0.75rem 0.875rem; border-radius: var(--radius);
  background: color-mix(in oklab, var(--error) 12%, var(--bg));
  color: var(--error); font-size: 0.875rem;
  border: 1px solid color-mix(in oklab, var(--error) 30%, transparent); }
/* Subtitle sits directly under h1; tighter coupling than .hint under input. */
.subtitle { margin: -0.5rem 0 1.5rem; color: var(--muted); font-size: 0.9375rem; }
.subtitle strong { color: var(--fg); font-weight: 600; }
.linkbtn { display: inline; background: none; border: 0; padding: 0;
  color: var(--focus); font: inherit; font-size: 0.875rem; cursor: pointer;
  text-decoration: underline; text-underline-offset: 2px; width: auto; }
.linkbtn:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: 2px; }
.aside { margin: 1rem 0 0; text-align: center; font-size: 0.875rem; color: var(--muted); }
.aside form { display: inline; }
.card { margin: 0 0 1.5rem; }
.card h2 { font-size: 1rem; font-weight: 600; margin: 0 0 0.5rem; color: var(--muted);
  text-transform: uppercase; letter-spacing: 0.04em; }
table { width: 100%; border-collapse: collapse; font-size: 0.9375rem; }
th, td { padding: 0.5rem 0.5rem; border-bottom: 1px solid var(--border); text-align: left; }
th { font-weight: 500; color: var(--muted); }
.num { text-align: right; font-variant-numeric: tabular-nums; }
.bell {
  position: fixed; top: 1rem; right: 1rem; z-index: 10;
  display: inline-flex; align-items: center; justify-content: center;
  width: 2.25rem; height: 2.25rem; border-radius: 999px;
  color: var(--fg); text-decoration: none;
  background: color-mix(in oklab, var(--bg) 92%, var(--fg));
}
.bell:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
.bell svg { width: 1.25rem; height: 1.25rem; }
.bell .badge {
  position: absolute; top: -0.25rem; right: -0.25rem;
  min-width: 1.125rem; height: 1.125rem; padding: 0 0.25rem;
  border-radius: 999px;
  background: var(--error, #b91c1c); color: #fff;
  font-size: 0.6875rem; font-weight: 700;
  display: inline-flex; align-items: center; justify-content: center;
  font-variant-numeric: tabular-nums;
}
.bell .badge:empty { display: none; }
.notifs { list-style: none; margin: 0; padding: 0; }
.notif { display: flex; gap: 0.75rem; padding: 0.75rem 0; border-bottom: 1px solid var(--border); }
.notif:last-child { border-bottom: 0; }
.notif.unread .notif-link strong { color: var(--fg); }
.notif:not(.unread) .notif-link strong { color: var(--muted); font-weight: 500; }
.notif-link { flex: 1; display: block; text-decoration: none; color: inherit; }
.notif-body { display: block; font-size: 0.875rem; color: var(--muted); margin-top: 0.25rem; }
.notif-time { display: block; font-size: 0.8125rem; color: var(--muted); margin-top: 0.25rem; }
.notif-actions { flex-shrink: 0; }
.filter { display: flex; gap: 0.75rem; align-items: end; margin: 0 0 1.5rem; flex-wrap: wrap; }
.filter label { display: flex; flex-direction: column; gap: 0.25rem; font-size: 0.875rem; color: var(--muted); }
.filter input { width: auto; min-width: 9rem; }
.filter button { width: auto; padding: 0.5rem 1rem; }
.reversal td { color: var(--muted); text-decoration: line-through; }
.badge-rev { display: inline-block; padding: 0.0625rem 0.375rem; border-radius: 999px;
  background: color-mix(in oklab, var(--muted) 18%, var(--bg));
  color: var(--muted); font-size: 0.6875rem; font-weight: 600;
  text-decoration: none; margin-left: 0.5rem; }
</style>
</head>
<body>
<main>
`

const layoutClose = `
</main>
{{if .SignedIn}}
<a class="bell" href="/notifications" aria-label="Notifications{{if .UnreadCount}} ({{.UnreadCount}} unread){{end}}">
<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
     stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
<path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9"/>
<path d="M10.3 21a1.94 1.94 0 0 0 3.4 0"/>
</svg>
<span class="badge">{{if .UnreadCount}}{{.UnreadCount}}{{end}}</span>
</a>
{{end}}
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
<button type="submit">Continue with Telegram</button>
</form>`

const verifyBody = `<h1>Enter your code</h1>
<p class="subtitle">Sent to <strong>{{.Identifier}}</strong> on Telegram. Code expires in 5 minutes.</p>
{{if .Error}}<p class="alert" role="alert">{{.Error}}</p>{{end}}
<form method="post" action="/verify">
<input type="hidden" name="identifier" value="{{.Identifier}}">
<div class="field">
<label for="code">6-digit code</label>
<input id="code" name="code" inputmode="numeric"
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
<p class="subtitle">Nothing to read.</p>
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
{{end}}
<p class="aside"><a class="linkbtn" href="/">&larr; Home</a></p>`

const posBody = `{{if .NotFound}}
<h1>Pos not found</h1>
<p class="subtitle">No Pos with that id, or it has been removed.</p>
<p class="aside"><a class="linkbtn" href="/">&larr; Home</a></p>
{{else}}
<h1>{{.Name}}{{if .Archived}} <span class="badge-rev">archived</span>{{end}}</h1>
<p class="subtitle">{{.Currency}}{{if .HasTarget}} &middot; target {{.Target}}{{end}}</p>

{{if .LoadError}}
<p class="alert" role="alert">Some data could not be loaded. The view may be incomplete.</p>
{{end}}

<section class="card">
<h2>Balance</h2>
<table>
<thead><tr><th>Cash</th><th class="num">Receivables</th><th class="num">Payables</th></tr></thead>
<tbody>
<tr>
<td class="num">&mdash;</td>
<td class="num">{{.Receivables}}</td>
<td class="num">{{.Payables}}</td>
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
<td>{{.Direction}}</td>
<td><a href="/pos/{{.OtherPosID}}">{{.OtherPosID}}</a></td>
<td class="num">{{.Outstanding}} {{.Currency}}</td>
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
<table>
<thead><tr><th>Date</th><th>Type</th><th class="num">Amount</th><th>Account</th><th>Counterparty</th><th>Note</th></tr></thead>
<tbody>
{{range .Transactions}}
<tr{{if .IsReversal}} class="reversal"{{end}}>
<td>{{.EffectiveDate}}</td>
<td>{{.Type}}{{if .IsReversal}} <a class="badge-rev" href="/transactions/{{.ReversesID}}">reverses</a>{{end}}</td>
<td class="num">{{.Amount}}</td>
<td>{{if .AccountName}}{{.AccountName}}{{else}}&mdash;{{end}}</td>
<td>{{if .CounterpartyName}}{{.CounterpartyName}}{{else}}&mdash;{{end}}</td>
<td>{{.Note}}</td>
</tr>
{{end}}
</tbody>
</table>
</section>
{{else}}
<p class="subtitle">No transactions for this Pos yet.</p>
{{end}}

<p class="aside"><a class="linkbtn" href="/">&larr; Home</a></p>
{{end}}`

const transactionsBody = `<h1>Transactions</h1>
<form method="get" action="/transactions" class="filter">
<label>From <input type="date" name="from" value="{{.From}}"></label>
<label>To <input type="date" name="to" value="{{.To}}"></label>
<button type="submit">Filter</button>
</form>
{{if .LoadError}}
<p class="alert" role="alert">Couldn&rsquo;t load transactions. Refresh in a moment.</p>
{{else if not .Items}}
<p class="subtitle">No transactions in this range.</p>
{{else}}
<table>
<thead><tr>
<th>Date</th><th>Type</th><th class="num">Amount</th>
<th>Account</th><th>Pos</th><th>Counterparty</th><th>Note</th>
</tr></thead>
<tbody>
{{range .Items}}
<tr{{if .IsReversal}} class="reversal"{{end}}>
<td>{{.EffectiveDate}}</td>
<td>{{.Type}}{{if .IsReversal}} <a class="badge-rev" href="/transactions/{{.ReversesID}}">reverses</a>{{end}}</td>
<td class="num">{{.Amount}}{{if .Currency}} {{.Currency}}{{end}}</td>
<td>{{if .AccountName}}{{.AccountName}}{{else}}&mdash;{{end}}</td>
<td>{{if .PosName}}{{.PosName}}{{else}}&mdash;{{end}}</td>
<td>{{if .CounterpartyName}}{{.CounterpartyName}}{{else}}&mdash;{{end}}</td>
<td>{{.Note}}</td>
</tr>
{{end}}
</tbody>
</table>
{{end}}
<p class="aside"><a class="linkbtn" href="/">&larr; Home</a></p>`

const homeBody = `<h1>Hi, {{.DisplayName}}</h1>
{{if .LoadError}}
<p class="alert" role="alert">Couldn&rsquo;t load your accounts and pos right now. Refresh in a moment.</p>
{{else if and (not .Accounts) (not .PosByCurrency)}}
<p class="subtitle">Accounts and Pos load once seed data lands. Balance computation wires up next.</p>
{{end}}

{{if .Accounts}}
<section class="card">
<h2>Accounts</h2>
<table>
<thead><tr><th>Name</th><th class="num">Balance</th></tr></thead>
<tbody>
{{range .Accounts}}
<tr><td>{{.Name}}</td><td class="num">&mdash;</td></tr>
{{end}}
</tbody>
</table>
</section>
{{end}}

{{range .PosByCurrency}}
<section class="card">
<h2>Pos &mdash; {{.Currency}}</h2>
<table>
<thead><tr><th>Name</th><th class="num">Cash</th><th class="num">Target</th></tr></thead>
<tbody>
{{range .Items}}
<tr>
  <td>{{.Name}}</td>
  <td class="num">&mdash;</td>
  <td class="num">{{if .HasTarget}}{{.Target}}{{else}}&mdash;{{end}}</td>
</tr>
{{end}}
</tbody>
</table>
</section>
{{end}}

<form method="post" action="/logout" class="aside">
<button type="submit" class="linkbtn">Sign out</button>
</form>`
