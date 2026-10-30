// Package template owns the html/template definitions for the web layer.
// Each page is a complete document parsed into its own template — there
// is no shared "body" block (which would collide across pages in a single
// template set). Layout chrome is shared via Go string concatenation,
// keeping all template strings as Go consts (no filesystem dependency).
package template

import (
	"encoding/json"
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
	template.Must(t.New("debts").Parse(layoutOpen + debtsBody + layoutClose))
	template.Must(t.New("recap").Parse(layoutOpen + recapBody + layoutClose))
	template.Must(t.New("pivot").Parse(layoutOpen + pivotBody + layoutClose))
	template.Must(t.New("income_templates").Parse(layoutOpen + incomeTemplatesListBody + layoutClose))
	template.Must(t.New("income_template_new").Parse(layoutOpen + incomeTemplateNewBody + layoutClose))
	template.Must(t.New("income_template_detail").Parse(layoutOpen + incomeTemplateDetailBody + layoutClose))
	template.Must(t.New("income_template_preview").Parse(layoutOpen + incomeTemplatePreviewBody + layoutClose))
	template.Must(t.New("settings").Parse(layoutOpen + settingsBody + layoutClose))
	template.Must(t.New("accounts").Parse(layoutOpen + accountsBody + layoutClose))
	template.Must(t.New("account_new").Parse(layoutOpen + accountNewBody + layoutClose))
	template.Must(t.New("search").Parse(layoutOpen + searchBody + layoutClose))
	template.Must(t.New("loan_login").Parse(layoutOpen + loanLoginBody + layoutClose))
	template.Must(t.New("loan_view").Parse(layoutOpen + loanViewBody + layoutClose))
	template.Must(t.New("loan_setup").Parse(layoutOpen + loanSetupBody + layoutClose))
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

// SpendingColumn is one of the top-N pos. AccountName disambiguates
// duplicate Pos names held in different accounts.
type SpendingColumn struct {
	PosID       string
	Name        string
	AccountName string
	Currency    string
	Total       int64 // sum across the date range
}

// SpendingRow is one month, with a cell per top-N pos plus a row total.
type SpendingRow struct {
	Month string  // "Apr 2026"
	Cells []int64 // amounts in column order; zero-filled for months with no data
	Total int64
}

// DebtsData drives the "Sisa Hutang" page: every open inter-Pos
// obligation with its outstanding remainder, plus per-currency totals.
// LoadError is the transient-DB-failure state; empty Items is the
// legitimate all-repaid case.
type DebtsData struct {
	Title       string
	DisplayName string
	UnreadCount int
	Items       []DebtRow
	Totals      []DebtTotal
	LoadError   bool
}

func (d DebtsData) SignedIn() bool { return d.DisplayName != "" }
func (d DebtsData) Compact() bool  { return false }
func (d DebtsData) Wide() bool     { return false }
func (d DebtsData) HideBell() bool { return false }
func (d DebtsData) Route() string  { return "debts" }

// DebtRow is one open obligation: DebtorPos owes CreditorPos.
type DebtRow struct {
	ID          string
	CreditorPos string
	DebtorPos   string
	Currency    string
	Owed        int64
	Repaid      int64
	Outstanding int64
	CreatedAt   time.Time
}

// DebtTotal is the outstanding remainder summed per currency.
type DebtTotal struct {
	Currency    string
	Outstanding int64
}

// RecapData drives the adjustable "Rekap" page: bucketed money_in /
// money_out sums per currency. Bucket is week|month|year; From/To are
// YYYY-MM-DD. Errors re-renders with messages (human page, never 400).
type RecapData struct {
	Title       string
	DisplayName string
	UnreadCount int
	Bucket      string
	From        string
	To          string
	Totals      []RecapTotal // one per currency, each carrying its rows
	Errors      []string
	LoadError   bool
}

func (d RecapData) SignedIn() bool { return d.DisplayName != "" }
func (d RecapData) Compact() bool  { return false }
func (d RecapData) Wide() bool     { return true }
func (d RecapData) HideBell() bool { return false }
func (d RecapData) Route() string  { return "recap" }

// TotalByCurrency returns the Totals entry for currency, appending a
// fresh one on first use. Handlers accumulate into it directly.
func (d *RecapData) TotalByCurrency(currency string) *RecapTotal {
	for i := range d.Totals {
		if d.Totals[i].Currency == currency {
			return &d.Totals[i]
		}
	}
	d.Totals = append(d.Totals, RecapTotal{Currency: currency})
	return &d.Totals[len(d.Totals)-1]
}

// RecapTotal is one currency section: totals plus newest-first rows.
type RecapTotal struct {
	Currency string
	Inflow   int64
	Outflow  int64
	Net      int64
	Rows     []RecapRow
}

// RecapRow is one bucket (week start / "Jan 2006" / "2006").
type RecapRow struct {
	Period  string
	Inflow  int64
	Outflow int64
	Net     int64
}

// PivotAccount is one Akun in the roll-forward filter checklist.
type PivotAccount struct {
	ID   string
	Name string
}

// PivotPosition is one (Akun, Kategori) identity in the filter. The pair
// is the identity: two positions sharing a name under different Akun
// are separate entries, each visually tied to its Akun.
type PivotPosition struct {
	PosID       string
	Name        string
	AccountID   string
	AccountName string
}

// PivotAkunGroup clusters filter positions under their Akun so the
// checklist never shows a bare, ambiguous name.
type PivotAkunGroup struct {
	AccountID   string
	AccountName string
	Positions   []PivotPosition
}

// PivotMovement is one signed movement row: money_in positive,
// money_out negative. Nama is the display label (note, counterparty, or
// date fallback); Date is YYYY-MM-DD for lexicographic client filtering.
type PivotMovement struct {
	ID          string
	PosID       string
	AccountID   string
	AccountName string
	PosName     string
	Nama        string
	Date        string
	Amount      int64
	Type        string
}

// PivotData drives the roll-forward report. Movements carry the full
// signed dataset as embedded JSON; every filter re-renders client-side
// without a reload, with the state kept in the URL.
type PivotData struct {
	Title       string
	DisplayName string
	UnreadCount int
	Accounts    []PivotAccount
	Positions   []PivotPosition
	Groups      []PivotAkunGroup
	Movements   []PivotMovement
	LoadError   bool
	// SavedFilters is the user's last persisted filter query string
	// (no leading '?'). A fresh visit with no URL params restores it;
	// explicit URL params always win for the current view.
	SavedFilters string
}

func (d PivotData) SignedIn() bool { return d.DisplayName != "" }
func (d PivotData) Compact() bool  { return false }
func (d PivotData) Wide() bool     { return false }
func (d PivotData) HideBell() bool { return false }
func (d PivotData) Route() string  { return "pivot" }

// GroupFor returns the Akun group for accountID, appending it on first
// use. The handler feeds positions in (Akun, Kategori) order so groups
// come out sorted without a second pass.
func (d *PivotData) GroupFor(accountID string) *PivotAkunGroup {
	for i := range d.Groups {
		if d.Groups[i].AccountID == accountID {
			return &d.Groups[i]
		}
	}
	d.Groups = append(d.Groups, PivotAkunGroup{AccountID: accountID})
	return &d.Groups[len(d.Groups)-1]
}

// SavedFiltersJSON embeds the persisted filter query string as a JSON
// string so the client can replay it through the same parse path as URL
// params. Empty saves encode as "".
func (d PivotData) SavedFiltersJSON() template.JS {
	b, err := json.Marshal(d.SavedFilters)
	if err != nil {
		return template.JS(`""`)
	}
	return template.JS(b)
}

// DataJSON embeds the client-side dataset (accounts, positions,
// movements) in a single JSON script tag. Empty slices encode as []
// so the client can iterate without nil checks.
func (d PivotData) DataJSON() template.JS {
	accounts := d.Accounts
	if accounts == nil {
		accounts = []PivotAccount{}
	}
	positions := d.Positions
	if positions == nil {
		positions = []PivotPosition{}
	}
	movements := d.Movements
	if movements == nil {
		movements = []PivotMovement{}
	}
	b, err := json.Marshal(map[string]any{
		"accounts":  accounts,
		"positions": positions,
		"movements": movements,
	})
	if err != nil {
		return template.JS(`{"accounts":[],"positions":[],"movements":[]}`)
	}
	return template.JS(b)
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

	// Loan fields — populated only when the Pos is a loan (pos.is_loan).
	IsLoan           bool
	LoanRepaid       int64 // = Cash (fund+disburse net to 0; repayments climb)
	LoanOutstanding  int64 // Target − LoanRepaid
	LoanPctRepaid    int   // 0..100
	HasBorrowerLogin bool
	BorrowerUsername string
	BorrowerLoginURL string // /loan/:id/login
	LoanSubmissions  []LoanSubmissionRow
	LoanPendingCount int
	Flash            string // generic flash (loan approve/reject)
	Error            string
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
	Templates        []IncomeTemplateOption // monthly-income shortcut (money_in only)
	Errors           []string
}

func (d TransactionNewData) SignedIn() bool   { return d.DisplayName != "" }
func (d TransactionNewData) Compact() bool    { return false }
func (d TransactionNewData) Wide() bool       { return false }
func (d TransactionNewData) HideBell() bool   { return false }
func (d TransactionNewData) Route() string    { return "transactions" }
func (d TransactionNewData) IsIncoming() bool { return d.Type == "money_in" }

// SearchData backs /search — the global search results page driven by the
// top-bar box. Each entity slice is pre-capped by its SQL query so the
// results panel stays balanced. Total lets the template branch between the
// results view and the "no matches" empty state without re-summing.
type SearchData struct {
	Title           string
	DisplayName     string
	UnreadCount     int
	Error           string
	Query           string
	Accounts        []SearchAccountRow
	Pos             []SearchPosRow
	Transactions    []SearchTxnRow
	Counterparties  []SearchCounterpartyRow
	IncomeTemplates []SearchIncomeRow
	Total           int
}

func (d SearchData) SignedIn() bool { return d.DisplayName != "" }
func (d SearchData) Compact() bool  { return false }
func (d SearchData) Wide() bool     { return false }
func (d SearchData) HideBell() bool { return false }

// Route returns "" so no sidebar item is marked current — search has no
// dedicated nav entry; it lives in the top bar.
func (d SearchData) Route() string { return "" }

type SearchAccountRow struct {
	ID   string
	Name string
}

type SearchPosRow struct {
	ID          string
	Name        string
	Currency    string
	AccountName string
}

type SearchIncomeRow struct {
	ID   string
	Name string
}

type SearchCounterpartyRow struct {
	Name string
}

// SearchTxnRow is a flattened transaction hit. The handler resolves the
// nullable joined columns into plain fields; Amount/Currency stay raw so the
// template formats them with the shared money func. Label is the note, or the
// counterparty name when the note is blank.
type SearchTxnRow struct {
	PosID        string
	Label        string
	PosName      string
	Counterparty string
	Type         string
	Amount       int64
	Currency     string
	Date         string
}

// ── Loan feature view models ──────────────────────────────────────────
// Borrower-facing pages render with SignedIn()==false so they reuse the
// minimal centered layout (no family sidebar/topbar). They never expose
// any data beyond the one loan the borrower is authenticated against.

// LoanLoginData drives the per-loan borrower sign-in form. PosID scopes the
// form action to /loan/:id/login; nothing about the loan is revealed pre-auth.
type LoanLoginData struct {
	Title string
	PosID string
	Error string
}

func (d LoanLoginData) SignedIn() bool { return false }
func (d LoanLoginData) Compact() bool  { return true }
func (d LoanLoginData) Wide() bool     { return false }
func (d LoanLoginData) HideBell() bool { return false }
func (d LoanLoginData) Route() string  { return "" }

// LoanViewData is the borrower's authenticated view of their single loan:
// outstanding balance, repayment progress (0→target), a submit form, and
// their own submission history.
type LoanViewData struct {
	Title        string
	PosID        string
	LoanName     string
	BorrowerName string
	Currency     string
	Target       int64
	Balance      int64 // repaid so far
	Outstanding  int64 // target − balance
	PctRepaid    int   // 0..100
	TodayISO     string
	Submissions  []LoanSubmissionRow
	Flash        string
	Error        string
}

func (d LoanViewData) SignedIn() bool { return false }
func (d LoanViewData) Compact() bool  { return false }
func (d LoanViewData) Wide() bool     { return false }
func (d LoanViewData) HideBell() bool { return false }
func (d LoanViewData) Route() string  { return "" }

// LoanSubmissionRow is one repayment submission as shown to either side.
type LoanSubmissionRow struct {
	ID        string
	PayerName string
	Amount    int64
	Date      string
	Note      string
	Status    string // pending | approved | rejected | cancelled
	CanCancel bool   // borrower may cancel their own pending rows
	Reason    string // reject reason, when status == rejected
}

// LoanSetupData drives the family-only "Set up loan" form (/loans/new),
// which creates the Pos, funds + disburses it, and sets the borrower login.
type LoanSetupData struct {
	Title        string
	DisplayName  string
	UnreadCount  int
	Accounts     []AccountOption
	Name         string
	BorrowerName string
	AmountRaw    string
	AccountID    string
	FundedFrom   string
	Username     string
	Errors       []string
}

func (d LoanSetupData) SignedIn() bool { return d.DisplayName != "" }
func (d LoanSetupData) Compact() bool  { return false }
func (d LoanSetupData) Wide() bool     { return false }
func (d LoanSetupData) HideBell() bool { return false }
func (d LoanSetupData) Route() string  { return "" }

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

// PosRow is one row in a per-currency Pos table. AccountName
// disambiguates duplicate Pos names held in different accounts.
type PosRow struct {
	ID          string // links the row to /pos/:id detail page
	Name        string
	AccountName string
	Cash        int64 // unit = the group's currency's smallest unit; zero until wired
	Target      int64
	HasTarget   bool
}

// ─── Icons ──────────────────────────────────────────────────────────
// Inline stroke icons for the sidebar and topbar (Lucide-style: 24×24
// viewBox, currentColor stroke). No emoji, no external assets — they
// follow the theme automatically and stay crisp on all densities.
// Decorative only (aria-hidden); the link text carries the label.
const iconSVGOpen = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">`
const iconSVGClose = `</svg>`

const iconHome = iconSVGOpen + `<path d="M3 10.5 12 3l9 7.5"/><path d="M5 9.5V21h14V9.5"/><path d="M9 21v-6h6v6"/>` + iconSVGClose
const iconTransactions = iconSVGOpen + `<path d="M8 3 4 7l4 4"/><path d="M4 7h16"/><path d="m16 21 4-4-4-4"/><path d="M20 17H4"/>` + iconSVGClose
const iconSpending = iconSVGOpen + `<path d="M3 3v18h18"/><path d="M8 17v-6M13 17V7M18 17v-3"/>` + iconSVGClose
const iconRecap = iconSVGOpen + `<path d="m3 17 6-6 4 4 8-8"/><path d="M15 7h6v6"/>` + iconSVGClose
const iconPivot = iconSVGOpen + `<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 10h18"/><path d="M10 10v10"/>` + iconSVGClose
const iconAccounts = iconSVGOpen + `<path d="M3 9.5 12 4l9 5.5"/><path d="M5 10v8M9.5 10v8M14.5 10v8M19 10v8"/><path d="M3 20h18"/>` + iconSVGClose
const iconDebts = iconSVGOpen + `<rect x="3" y="6" width="18" height="13" rx="2"/><path d="M3 10.5h18"/><path d="M7 15h4"/>` + iconSVGClose
const iconIncome = iconSVGOpen + `<path d="M14 3H7a1 1 0 0 0-1 1v16a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1V8z"/><path d="M14 3v5h5"/><path d="M9 13h6M9 17h6"/>` + iconSVGClose
const iconPos = iconSVGOpen + `<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>` + iconSVGClose
const iconBell = iconSVGOpen + `<path d="M18 9a6 6 0 1 0-12 0c0 6-2.5 7-2.5 7h17S18 15 18 9"/><path d="M10 20a2.2 2.2 0 0 0 4 0"/>` + iconSVGClose
const iconSettings = iconSVGOpen + `<path d="M4 7h9M17 7h3M4 17h3M11 17h9"/><circle cx="15" cy="7" r="2"/><circle cx="9" cy="17" r="2"/>` + iconSVGClose
const iconMenu = iconSVGOpen + `<path d="M4 7h16M4 12h16M4 17h16"/>` + iconSVGClose
const iconPlus = iconSVGOpen + `<path d="M12 5v14M5 12h14"/>` + iconSVGClose
const iconMinus = iconSVGOpen + `<path d="M5 12h14"/>` + iconSVGClose
const iconCheck = iconSVGOpen + `<path d="M20 6 9 17l-5-5"/>` + iconSVGClose
const iconCheckDouble = iconSVGOpen + `<path d="m18 6-9 9-5-5"/><path d="m22 11-9 9-5-5"/>` + iconSVGClose
const iconCheckSquare = iconSVGOpen + `<rect x="3" y="3" width="18" height="18" rx="2"/><path d="m9 12 2 2 4-4"/>` + iconSVGClose
const iconX = iconSVGOpen + `<path d="M18 6 6 18M6 6l12 12"/>` + iconSVGClose
const iconSearch = iconSVGOpen + `<circle cx="11" cy="11" r="7"/><path d="m21 21-4.3-4.3"/>` + iconSVGClose
const iconLogin = iconSVGOpen + `<path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4"/><path d="m10 17 5-5-5-5"/><path d="M15 12H3"/>` + iconSVGClose
const iconLogout = iconSVGOpen + `<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><path d="m16 17 5-5-5-5"/><path d="M21 12H9"/>` + iconSVGClose
const iconSend = iconSVGOpen + `<path d="m22 2-7 20-4-9-9-4z"/><path d="M22 2 11 13"/>` + iconSVGClose
const iconRefresh = iconSVGOpen + `<path d="M21 12a9 9 0 1 1-2.6-6.4"/><path d="M21 3v6h-6"/>` + iconSVGClose
const iconFilter = iconSVGOpen + `<path d="M22 3H2l8 9.5V19l4 2v-8.5z"/>` + iconSVGClose
const iconReset = iconSVGOpen + `<path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/>` + iconSVGClose
const iconArchive = iconSVGOpen + `<rect x="3" y="4" width="18" height="5" rx="1"/><path d="M5 9v11h14V9"/><path d="M10 13h4"/>` + iconSVGClose
const iconTrash = iconSVGOpen + `<path d="M4 7h16"/><path d="M9 7V5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/><path d="M6 7l1 13a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1l1-13"/><path d="M10 11v6M14 11v6"/>` + iconSVGClose
const iconEye = iconSVGOpen + `<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7z"/><circle cx="12" cy="12" r="3"/>` + iconSVGClose
const iconCalendar = iconSVGOpen + `<rect x="3" y="5" width="18" height="16" rx="2"/><path d="M8 3v4M16 3v4M3 10h18"/>` + iconSVGClose
const iconClock = iconSVGOpen + `<circle cx="12" cy="12" r="8.5"/><path d="M12 7v5l3 2"/>` + iconSVGClose
const iconLayers = iconSVGOpen + `<path d="m12 2 9 5-9 5-9-5z"/><path d="m3 12 9 5 9-5"/><path d="m3 17 9 5 9-5"/>` + iconSVGClose
const iconArrowDown = iconSVGOpen + `<path d="M12 5v14"/><path d="m19 12-7 7-7-7"/>` + iconSVGClose
const iconArrowUp = iconSVGOpen + `<path d="M12 19V5"/><path d="m5 12 7-7 7 7"/>` + iconSVGClose
const iconBanknote = iconSVGOpen + `<rect x="3" y="7" width="18" height="11" rx="2"/><circle cx="12" cy="12.5" r="2.5"/>` + iconSVGClose
const iconChevDown = iconSVGOpen + `<path d="m6 9 6 6 6-6"/>` + iconSVGClose
const iconChevUp = iconSVGOpen + `<path d="m18 15-6-6-6 6"/>` + iconSVGClose
const iconSun = iconSVGOpen + `<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>` + iconSVGClose
const iconMoon = iconSVGOpen + `<path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/>` + iconSVGClose
const iconMonitor = iconSVGOpen + `<rect x="2" y="4" width="20" height="13" rx="2"/><path d="M8 21h8M12 17v4"/>` + iconSVGClose
const iconPencil = iconSVGOpen + `<path d="M17 3a2.8 2.8 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5z"/>` + iconSVGClose

const layoutOpen = `<!doctype html>
<html lang="en"{{themeAttr}}>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>Shima &mdash; {{.Title}}</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
<link rel="stylesheet" href="/static/pico.min.css">
<style>
/* Pico v2 base is loaded above (vendored at web/static/pico.min.css for
 * offline dev). The block below maps Pico's custom properties onto Shima
 * tokens and tightens everything to a compact Google-Sheets density.
 * Generic element resets, typography scale and form primitives come from
 * Pico; only Shima layout + app components live here. */
/* shadcn-style design tokens — adapted for plain CSS (no Tailwind).
 * Neutral scale follows shadcn's zinc; the primary is green (green-700
 * in light mode so primary text on white meets WCAG AA ≥4.5:1,
 * green-400 in dark mode) instead of shadcn's default near-black.
 * Functional: success green-600, warning amber-600, error red-600.
 */
:root {
  /* Brand / interactive — green-700 (light) so primary text on white
   * meets WCAG AA; green-400 in dark mode. */
  --primary:        #15803D;
  --primary-hover:  #166534;
  --primary-active: #14532D;
  --primary-bg:     #F0FDF4;
  --primary-foreground: #FFFFFF;

  /* Functional */
  --success: #16A34A;
  --warning: #D97706;
  --error:   #DC2626;
  --error-bg:#FEF2F2;
  --error-border:#FECACA;
  --info:        #1D4ED8;
  --info-bg:     #EFF6FF;
  --info-border: #BFDBFE;

  /* Neutral text + surfaces (light mode; zinc scale à la shadcn) */
  --text:           #09090B;
  --text-secondary: #52525B;
  --text-tertiary:  #A1A1AA;
  --border:         #E4E4E7;
  --border-secondary:#F4F4F5;
  --bg-container:   #FFFFFF;
  --bg-page:        #FAFAFA;
  --bg-elevated:    #FFFFFF;
  --bg-fill:        #F4F4F5;

  --radius:    8px;
  --radius-sm: 6px;
  --radius-lg: 12px;

  --shadow-sm: 0 1px 2px 0 rgb(0, 0, 0, 0.05);

  --font-sm: 11px; --font-base: 12px; --font-lg: 14px;
  --font-h5: 14px; --font-h4: 16px; --font-h3: 18px; --font-h2: 22px; --font-h1: 28px;

  accent-color: var(--primary);
}
/* Dark-mode tokens — used by both the @media auto path AND the
 * explicit :root[data-theme="dark"] override below. Defined once via
 * a custom-property block we can re-apply. */
@media (prefers-color-scheme: dark) {
  :root {
    --primary:        #4ADE80;
    --primary-hover:  #86EFAC;
    --primary-active: #22C55E;
    --primary-bg:     #0F2A1A;
    --primary-foreground: #052E16;

    --success: #4ADE80;
    --warning: #FBBF24;
    --error:    #F87171;
    --error-bg: #2C1618;
    --error-border:#5C2223;
    --info:        #93C5FD;
    --info-bg:     #172554;
    --info-border: #1E3A8A;

    --text:           #FAFAF9;
    --text-secondary: #A1A1AA;
    --text-tertiary:  #71717A;
    --border:         #27272A;
    --border-secondary:#1F1F23;
    --bg-container:   #18181B;
    --bg-page:        #09090B;
    --bg-elevated:    #18181B;
    --bg-fill:        #27272A;
  }
  .chip-in       { color: var(--success); background: color-mix(in oklab, var(--success) 12%, transparent); border-color: color-mix(in oklab, var(--success) 30%, transparent); }
  .chip-out      { color: var(--error); background: color-mix(in oklab, var(--error) 12%, transparent); border-color: color-mix(in oklab, var(--error) 30%, transparent); }
  .chip-transfer { color: var(--info); background: color-mix(in oklab, var(--info) 12%, transparent); border-color: color-mix(in oklab, var(--info) 30%, transparent); }
}
/* Explicit user choice — beats the @media query.
 *   data-theme="light"  forces light even when OS prefers dark
 *   data-theme="dark"   forces dark even when OS prefers light
 *   no attribute        falls through to @media (= "auto" / OS) */
:root[data-theme="light"] {
  --primary:        #15803D;
  --primary-hover:  #166534;
  --primary-active: #14532D;
  --primary-bg:     #F0FDF4;
  --primary-foreground: #FFFFFF;

  --success: #16A34A;
  --warning: #D97706;
  --error:    #DC2626;
  --error-bg: #FEF2F2;
  --error-border:#FECACA;
  --info:        #1D4ED8;
  --info-bg:     #EFF6FF;
  --info-border: #BFDBFE;

  --text:           #09090B;
  --text-secondary: #52525B;
  --text-tertiary:  #A1A1AA;
  --border:         #E4E4E7;
  --border-secondary:#F4F4F5;
  --bg-container:   #FFFFFF;
  --bg-page:        #FAFAFA;
  --bg-elevated:    #FFFFFF;
  --bg-fill:        #F4F4F5;
}
:root[data-theme="dark"] {
  --primary:        #4ADE80;
  --primary-hover:  #86EFAC;
  --primary-active: #22C55E;
  --primary-bg:     #0F2A1A;
  --primary-foreground: #052E16;

  --success: #4ADE80;
  --warning: #FBBF24;
  --error:    #F87171;
  --error-bg: #2C1618;
  --error-border:#5C2223;
  --info:        #93C5FD;
  --info-bg:     #172554;
  --info-border: #1E3A8A;

  --text:           #FAFAF9;
  --text-secondary: #A1A1AA;
  --text-tertiary:  #71717A;
  --border:         #27272A;
  --border-secondary:#1F1F23;
  --bg-container:   #18181B;
  --bg-page:        #09090B;
  --bg-elevated:    #18181B;
  --bg-fill:        #27272A;
}
:root[data-theme="dark"] .chip-in       { color: var(--success); background: color-mix(in oklab, var(--success) 12%, transparent); border-color: color-mix(in oklab, var(--success) 30%, transparent); }
:root[data-theme="dark"] .chip-out      { color: var(--error); background: color-mix(in oklab, var(--error) 12%, transparent); border-color: color-mix(in oklab, var(--error) 30%, transparent); }
:root[data-theme="dark"] .chip-transfer { color: var(--info); background: color-mix(in oklab, var(--info) 12%, transparent); border-color: color-mix(in oklab, var(--info) 30%, transparent); }
/* ── Pico v2 bridge: Shima tokens win, compact sheet density ──────────
 * Pico is loaded from /static/pico.min.css above. Pico scopes its own
 * --pico-* defaults under :root:not([data-theme=dark]) (specificity
 * 0,2,0), so a plain :root bridge loses to it regardless of document
 * order. This block repeats that scoping — :root:not([data-theme=dark])
 * plus :root[data-theme=dark] — to meet Pico at 0,2,0 and win on later
 * document order in every theme state: explicit dark, explicit light and
 * OS-auto (where <html> carries no data-theme). All theme-aware values go
 * through var(--primary) / var(--bg-*) so light, dark and OS-auto all
 * follow the shima_theme data-theme contract without duplicating the
 * palette. */
:root:not([data-theme=dark]),
:root[data-theme=dark] {
  --pico-font-family: "Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif;
  --pico-font-size: 12px;
  --pico-line-height: 1.45;
  --pico-border-radius: 4px;
  --pico-spacing: 0.5rem;
  --pico-typography-spacing-vertical: 0.5rem;
  --pico-block-spacing-vertical: 0;
  --pico-block-spacing-horizontal: 0.75rem;
  --pico-grid-column-gap: 0.5rem;
  --pico-grid-row-gap: 0.5rem;
  --pico-form-element-spacing-vertical: 0.375rem;
  --pico-form-element-spacing-horizontal: 0.5rem;
  --pico-nav-element-spacing-vertical: 0.5rem;
  --pico-nav-element-spacing-horizontal: 0.5rem;
  --pico-nav-link-spacing-vertical: 0.375rem;
  --pico-nav-link-spacing-horizontal: 0.5rem;
  --pico-background-color: var(--bg-page);
  --pico-color: var(--text);
  --pico-muted-color: var(--text-secondary);
  --pico-muted-border-color: var(--border-secondary);
  --pico-primary: var(--primary);
  --pico-primary-background: var(--primary);
  --pico-primary-border: var(--primary);
  --pico-primary-underline: var(--primary);
  --pico-primary-hover: var(--primary-hover);
  --pico-primary-hover-background: var(--primary-hover);
  --pico-primary-hover-border: var(--primary-hover);
  --pico-primary-hover-underline: var(--primary-hover);
  --pico-primary-focus: color-mix(in oklab, var(--primary) 35%, transparent);
  --pico-primary-inverse: var(--primary-foreground);
  --pico-secondary: var(--text-secondary);
  --pico-secondary-background: var(--bg-fill);
  --pico-secondary-border: var(--border);
  --pico-secondary-hover-background: var(--bg-fill);
  --pico-contrast: var(--text);
  --pico-table-border-color: var(--border);
  --pico-table-row-stripped-background-color: color-mix(in oklab, var(--primary) 4%, transparent);
  --pico-form-element-background-color: var(--bg-container);
  --pico-form-element-selected-background-color: var(--border-secondary);
  --pico-form-element-border-color: var(--border);
  --pico-form-element-color: var(--text);
  --pico-form-element-placeholder-color: var(--text-tertiary);
  --pico-form-element-active-background-color: var(--bg-container);
  --pico-form-element-active-border-color: var(--primary);
  --pico-form-element-focus-color: var(--primary);
  --pico-form-element-invalid-border-color: var(--error);
  --pico-form-element-invalid-active-border-color: var(--error);
  --pico-form-element-invalid-focus-color: var(--error);
  --pico-form-element-valid-border-color: var(--success);
  --pico-form-element-valid-active-border-color: var(--success);
  --pico-form-element-valid-focus-color: var(--success);
  --pico-ins-color: var(--success);
  --pico-del-color: var(--error);
  --pico-mark-background-color: color-mix(in oklab, var(--warning) 25%, transparent);
  --pico-mark-color: var(--text);
  --pico-code-background-color: var(--bg-fill);
  --pico-code-color: var(--text-secondary);
}
::selection { background: color-mix(in oklab, var(--primary) 25%, transparent); }
/* Pico already sets box-sizing + body margin; keep our explicit reset for
 * specificity without duplicating its preflight. */
html, body { margin: 0; padding: 0; }
html, body { max-width: 100%; overflow-x: clip; }
/* Every interactive element shows a visible keyboard focus ring. Pico
 * supplies its own :focus; this guarantees a green ring even where Pico
 * is subtle, and covers our custom rows (pivot-sub) and links. */
*:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 2px;
  border-radius: 2px;
}
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
  font-family: "Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto,
               "Helvetica Neue", Arial, "PingFang SC", "Hiragino Sans GB",
               "Microsoft YaHei", sans-serif;
  font-size: var(--font-base); line-height: 1.5714;
  -webkit-font-smoothing: antialiased; -moz-osx-font-smoothing: grayscale;
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
  min-width: 0;
  background: var(--bg-container);
  border-radius: 0;
  padding: 8px;
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
    grid-template-columns: 248px 1fr;
    grid-template-rows: auto 1fr;
    grid-template-areas:
      "topbar topbar"
      "sidebar main";
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
/* Top bar — persistent EventCatalog-style header spanning the full width. */
.topbar {
  grid-area: topbar;
  display: flex; align-items: center; gap: 10px;
  padding: 0 16px; height: 56px;
  background: var(--bg-container);
  border-bottom: 1px solid var(--border-secondary);
  position: sticky; top: 0; z-index: 40;
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
/* Brand lockup: green rounded logo tile + wordmark. */
.topbar-brand {
  display: inline-flex; align-items: center; gap: 10px;
  text-decoration: none; color: var(--text);
  font-weight: 700; font-size: 15px; letter-spacing: -0.01em;
}
.topbar-brand .logo {
  display: inline-flex; align-items: center; justify-content: center;
  width: 28px; height: 28px; border-radius: 7px;
  background: linear-gradient(135deg, var(--primary-hover), var(--primary));
  color: #fff; font-size: 15px;
  box-shadow: 0 1px 2px rgba(0,0,0,0.12);
}
/* Cosmetic command-palette-style search trigger (visual only, like
 * EventCatalog's ⌘K). Not wired to a search backend yet. */
.topbar-search {
  display: none; align-items: center; gap: 8px; margin: 0 0 0 12px;
  min-width: 0; flex: 0 1 320px;
  padding: 6px 10px; border-radius: var(--radius);
  border: 1px solid var(--border); background: var(--bg-fill);
  color: var(--text-tertiary); font-size: 13px; cursor: text;
}
.topbar-search:focus-within {
  border-color: var(--primary); background: var(--bg-container);
  box-shadow: 0 0 0 2px color-mix(in oklab, var(--primary) 18%, transparent);
}
.topbar-search input {
  flex: 1 1 auto; min-width: 0;
  border: 0; outline: none; padding: 0; margin: 0;
  background: transparent; color: var(--text);
  font: inherit; font-size: 13px;
}
.topbar-search input::placeholder { color: var(--text-tertiary); }
.topbar-search input::-webkit-search-cancel-button { -webkit-appearance: none; }
.topbar-search kbd {
  margin-left: auto;
  font-family: ui-monospace, "SF Mono", Menlo, Consolas, monospace;
  font-size: 11px; color: var(--text-tertiary);
  border: 1px solid var(--border); border-radius: 4px;
  padding: 1px 5px; background: var(--bg-container);
}
.topbar-actions { margin-left: auto; display: inline-flex; align-items: center; gap: 4px; }
.topbar-icon {
  display: inline-flex; align-items: center; justify-content: center;
  width: 38px; height: 38px; border-radius: var(--radius);
  color: var(--text-secondary); text-decoration: none; position: relative;
  font-size: 18px;
}
.topbar-icon:hover { background: var(--bg-fill); color: var(--primary); }
.topbar-icon .badge {
  position: absolute; top: 5px; right: 4px;
}

.sidebar {
  grid-area: sidebar;
  background: var(--bg-container);
  border-right: 1px solid var(--border-secondary);
  padding: 12px 12px 16px;
  display: flex; flex-direction: column; gap: 2px;
  /* Mobile: drawer pinned to the viewport edge, hidden until toggled. */
  position: fixed; top: 0; bottom: 0; left: 0; width: 264px;
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
/* Mobile drawer keeps a brand row; desktop hides it (brand lives in topbar). */
.sidebar-brand {
  font-weight: 700; color: var(--text);
  padding: 6px 12px 12px;
  border-bottom: 1px solid var(--border-secondary);
  margin: 0 0 8px;
}
/* Grouped navigation with small uppercase section labels. */
.nav-group { display: flex; flex-direction: column; gap: 2px; }
.nav-group-label {
  font-size: 11px; font-weight: 600; text-transform: uppercase;
  letter-spacing: 0.06em; color: var(--text-tertiary);
  padding: 0 12px; margin: 16px 0 4px;
}
.nav-group:first-of-type .nav-group-label { margin-top: 4px; }
.sidebar a, .sidebar form button {
  display: flex; align-items: center; gap: 10px;
  padding: 8px 12px; border-radius: var(--radius);
  color: var(--text-secondary); text-decoration: none;
  font-size: var(--font-base); white-space: nowrap;
  transition: background 0.15s, color 0.15s, box-shadow 0.15s;
}
.nav-ico {
  flex: none; width: 18px; height: 18px; opacity: 0.85;
}
.nav-ico svg { display: block; width: 18px; height: 18px; }
.topbar-icon svg { display: block; width: 20px; height: 20px; }
.hamburger svg { display: block; width: 22px; height: 22px; }
.sidebar a .badge { margin-left: auto; }
.sidebar a:hover { background: var(--bg-fill); color: var(--primary); }
.sidebar a:hover .nav-ico { opacity: 1; }
.sidebar a[aria-current="page"] {
  background: color-mix(in oklab, var(--primary) 12%, transparent);
  color: var(--primary); font-weight: 500;
}
.sidebar a[aria-current="page"] .nav-ico { opacity: 1; }
.sidebar-end { margin-top: auto; padding-top: 12px;
  border-top: 1px solid var(--border-secondary); }
.sidebar-end .linkbtn,
.sidebar form button {
  width: 100%;
  background: transparent; border: 0; box-shadow: none;
  color: var(--text-secondary);
  text-align: left; padding: 8px 12px;
  cursor: pointer; font: inherit;
}
.sidebar-end .linkbtn:hover,
.sidebar form button:hover { background: var(--bg-fill); color: var(--primary); }
/* Breadcrumb row at the top of content (EventCatalog-style). */
.breadcrumb {
  display: flex; align-items: center; gap: 6px; flex-wrap: wrap;
  font-size: var(--font-sm); color: var(--text-tertiary);
  margin: 0 0 16px;
}
.breadcrumb a { color: var(--text-tertiary); text-decoration: none; }
.breadcrumb a:hover { color: var(--primary); }
.breadcrumb .sep { opacity: 0.6; }
.breadcrumb .crumb-current { color: var(--text-secondary); font-weight: 500; }
/* Search results page. */
.search-page-form {
  display: flex; gap: 8px; align-items: stretch; margin: 0 0 24px;
}
.search-page-form input { flex: 1 1 auto; }
.search-page-form button { width: auto; flex: none; padding: 8px 20px; }
.search-group { margin-bottom: 16px; padding: 0; }
.search-count {
  margin-left: auto; font-size: var(--font-sm); font-weight: 600;
  color: var(--text-tertiary);
  background: var(--bg-fill); border-radius: 999px; padding: 1px 9px;
}
.search-result {
  display: flex; align-items: center; gap: 12px;
  padding: 12px 16px; text-decoration: none; color: var(--text);
  border-top: 1px solid var(--border-secondary);
  transition: background 0.12s;
}
.search-result:hover { background: var(--bg-fill); }
.search-result-ico {
  flex: none; width: 22px; text-align: center; font-size: 16px; opacity: 0.9;
}
.search-result-ico svg { display: block; width: 20px; height: 20px; }
.search-result-main { min-width: 0; display: flex; flex-direction: column; gap: 1px; }
.search-result-title {
  font-size: var(--font-base); font-weight: 500; color: var(--text);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.search-result-sub { font-size: var(--font-sm); color: var(--text-tertiary); }
.search-result-tag {
  margin-left: auto; flex: none; font-size: var(--font-sm);
  color: var(--text-tertiary); text-transform: uppercase; letter-spacing: 0.04em;
}
.search-result-amt {
  margin-left: auto; flex: none; font-weight: 600; font-size: var(--font-base);
  font-variant-numeric: tabular-nums;
}
.search-result-amt.amt-in { color: var(--success); }
.search-result-amt.amt-out { color: var(--text); }
.search-chips { display: flex; flex-wrap: wrap; gap: 8px; padding: 16px; }
.search-chip {
  font-size: var(--font-sm); color: var(--text-secondary);
  background: var(--bg-fill); border: 1px solid var(--border-secondary);
  border-radius: 999px; padding: 4px 12px;
}
/* Loan pages (borrower-facing + setup). */
.loan-brand { display: inline-flex; align-items: center; gap: 8px;
  font-weight: 700; font-size: 15px; color: var(--text); letter-spacing: -0.01em; }
.loan-logo { display: inline-flex; align-items: center; justify-content: center;
  width: 26px; height: 26px; border-radius: 7px; color: #fff; font-size: 14px;
  background: linear-gradient(135deg, var(--primary-hover), var(--primary)); }
.loan-topline { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.loan-summary { background: var(--primary-bg); border: 1px solid var(--border-secondary);
  border-radius: var(--radius-lg); padding: 20px; margin-bottom: 20px; }
.loan-summary-head { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; }
.loan-summary-label { font-size: var(--font-sm); text-transform: uppercase;
  letter-spacing: 0.05em; color: var(--text-tertiary); }
.loan-summary-amt { font-size: var(--font-h3); font-weight: 700; color: var(--primary);
  font-variant-numeric: tabular-nums; }
.loan-progress { height: 8px; border-radius: 999px; background: var(--bg-fill);
  overflow: hidden; margin: 14px 0 8px; }
.loan-progress-bar { height: 100%; border-radius: 999px; background: var(--primary);
  transition: width 0.3s ease; }
.loan-summary-meta { font-size: var(--font-sm); color: var(--text-secondary); }
.loan-card-body { padding: 16px; }
.loan-sub { display: flex; align-items: center; gap: 12px; padding: 12px 16px;
  border-top: 1px solid var(--border-secondary); }
.loan-sub-main { min-width: 0; display: flex; flex-direction: column; gap: 1px; }
.loan-sub-amt { font-weight: 600; font-variant-numeric: tabular-nums; }
.loan-sub-meta { font-size: var(--font-sm); color: var(--text-tertiary); }
.loan-sub-cancel { margin-left: 4px; }
.loan-sub-cancel button { width: auto; padding: 4px 8px; }
.loan-status { margin-left: auto; flex: none; font-size: var(--font-sm); font-weight: 600;
  text-transform: capitalize; border-radius: 999px; padding: 2px 10px; }
.loan-status-pending   { color: var(--warning); background: color-mix(in oklab, var(--warning) 14%, transparent); }
.loan-status-approved  { color: var(--success); background: color-mix(in oklab, var(--success) 14%, transparent); }
.loan-status-rejected  { color: var(--error);   background: var(--error-bg); }
.loan-status-cancelled { color: var(--text-tertiary); background: var(--bg-fill); }
.flash { margin: 0 0 16px; padding: 8px 12px; border-radius: var(--radius);
  background: var(--primary-bg); color: var(--primary); border: 1px solid color-mix(in oklab, var(--primary) 25%, transparent); font-size: var(--font-base); }
@media (min-width: 768px) {
  .topbar .hamburger { display: none; }
  .topbar-search { display: inline-flex; }
  .sidebar-overlay { display: none; }
  .sidebar-brand { display: none; }
  .sidebar {
    position: sticky; top: 56px; transform: none; transition: none;
    width: auto; z-index: auto;
    height: calc(100vh - 56px); align-self: start;
  }
  .sidebar-toggle:checked ~ .sidebar-overlay { display: none; }
}
h1 { font-size: var(--font-h3); font-weight: 600; line-height: 1.27;
  letter-spacing: -0.025em;
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

/* Primary button — shadcn-style: medium weight, subtle shadow,
 * ring on focus, no offset "3D" shadow. */
button {
  width: 100%; padding: 8px 16px; font: inherit; font-size: var(--font-base);
  font-weight: 500; line-height: 1.5714;
  color: var(--primary-foreground); background: var(--primary);
  border: 1px solid var(--primary); border-radius: var(--radius);
  cursor: pointer; transition: background 0.2s, border-color 0.2s;
  box-shadow: 0 1px 2px 0 rgb(0, 0, 0, 0.05);
}
button:not(.linkbtn):hover:not(:disabled) { background: var(--primary-hover); border-color: var(--primary-hover); }
button:not(.linkbtn):active:not(:disabled) { background: var(--primary-active); border-color: var(--primary-active); }
button:focus-visible { outline: none; box-shadow: 0 0 0 2px color-mix(in oklab, var(--primary) 25%, transparent); }
button:disabled {
  background: var(--bg-fill); color: var(--text-tertiary);
  border-color: var(--border); cursor: not-allowed; box-shadow: none;
}

/* Small action buttons for manage-list rows (Accounts, Pos): a shadcn
 * button hierarchy instead of full-width primaries. The doubled class
 * out-specifies the base button:not(.linkbtn):hover rule so each
 * variant keeps its own hover/active treatment. */
.btn-sm {
  width: auto; padding: 6px 12px;
  font-size: 13px; white-space: nowrap; box-shadow: none;
}
button.btn-outline.btn-sm {
  background: var(--bg-container); color: var(--text-secondary);
  border-color: var(--border);
}
button.btn-outline.btn-sm:hover:not(:disabled) {
  background: var(--bg-container); color: var(--primary);
  border-color: var(--primary);
}
button.btn-outline.btn-sm:active:not(:disabled) {
  background: var(--bg-fill); color: var(--primary-active);
  border-color: var(--primary-active);
}
button.btn-danger-outline.btn-sm {
  background: transparent; color: var(--error);
  border-color: var(--error-border);
}
button.btn-danger-outline.btn-sm:hover:not(:disabled),
button.btn-danger-outline.btn-sm:active:not(:disabled) {
  background: var(--error-bg); color: var(--error);
  border-color: var(--error);
}

/* Manage-list inline row forms (Accounts rename, Pos account move) and
 * row action clusters. Token-driven flex rows; the table-wrap scrolls
 * horizontally on narrow screens. */
.inline-row-form { display: flex; gap: 8px; align-items: center; min-width: 240px; }
.inline-row-form input, .inline-row-form select { flex: 1 1 auto; min-width: 0; }
.inline-row-actions { display: flex; gap: 6px; flex-wrap: wrap; }
.inline-row-actions form, form.inline-form { display: inline; }

/* Quiet inline tone for parenthetical hints inside labels and rows. */
.quiet { color: var(--text-tertiary); font-weight: 400; }
/* Card interior padding + common spacings, replacing one-off styles. */
.card-pad { padding: 16px; }
.card-pad > .subtitle { margin-top: 0; margin-bottom: 12px; }
.mt-4 { margin-top: 16px; }
.alert ul { margin: 8px 0 0 20px; padding: 0; }
.add-line { margin: 8px 0 0; }
.section-h2 { font-size: var(--font-base); font-weight: 600; margin: 24px 0 12px; }
.section-h3 { font-size: var(--font-sm); font-weight: 600; margin: 16px 0 8px; }

/* Base pill button (shadcn outline, small): applies everywhere, including
 * table-cell actions like loan approvals. Page-head and hero rules below
 * refine it where needed. */
.pill {
  display: inline-flex; align-items: center; justify-content: center; gap: 6px;
  width: auto; padding: 5px 12px;
  font-size: 12px; font-weight: 600; line-height: 1.5;
  color: var(--text); background: var(--bg-container);
  border: 1px solid var(--border); border-radius: var(--radius);
  box-shadow: var(--shadow-sm); cursor: pointer; text-decoration: none; white-space: nowrap;
  transition: border-color 0.15s, color 0.15s, background 0.15s;
}
a.pill:hover, button.pill:hover:not(:disabled) {
  color: var(--primary); border-color: var(--primary);
}
.pill.primary {
  color: var(--primary-foreground); background: var(--primary); border-color: var(--primary);
}
.pill.primary:hover { background: var(--primary-hover); border-color: var(--primary-hover); color: var(--primary-foreground); }

/* Content links use the primary green (never browser-default blue).
 * Badges keep their own tone. */
main table a:not(.badge-rev) { color: var(--primary); text-decoration: none; font-weight: 500; }
main table a:not(.badge-rev):visited { color: var(--primary); }
main table a:not(.badge-rev):hover { color: var(--primary-hover); text-decoration: underline; }

/* Icon + label buttons: every action button carries a leading icon.
 * Placed after .linkbtn so the flex layout wins ties for link-styled
 * buttons (linkbtn stays inline for text-only links). */
.btn-ico {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  white-space: nowrap;
}
.btn-ico svg { flex: none; width: 16px; height: 16px; }
.linkbtn.btn-ico { display: inline-flex; align-items: center; gap: 6px; }
.btn-ico.btn-sm { gap: 6px; }
.btn-ico.btn-sm svg { width: 14px; height: 14px; }
.pill svg { flex: none; width: 14px; height: 14px; }
.pivot-seg button { display: inline-flex; align-items: center; gap: 6px; }
.pivot-seg button svg { flex: none; width: 14px; height: 14px; }
.sidebar form button { display: flex; align-items: center; gap: 10px; }
.sidebar form button svg { display: block; flex: none; width: 18px; height: 18px; }

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

/* Google-Sheets grid: full cell borders, compact rows, sticky header.
 * Pico supplies the table primitive; this layer tightens it to sheet
 * density (2-4px vertical padding), forces gridlines on every cell,
 * right-aligns numbers in monospace tabular figures, and keeps long
 * rows on one line with the wrapper scrolling instead of the page. */
table {
  width: 100%; border-collapse: collapse; border-spacing: 0;
  font-size: var(--font-base); line-height: 1.45; color: var(--text);
  border: 1px solid var(--border);
  background: var(--bg-container);
  font-variant-numeric: tabular-nums;
}
thead th {
  background: var(--bg-fill); color: var(--text-secondary);
  font-weight: 600; font-size: 11px; padding: 3px 6px; text-align: left;
  border: 1px solid var(--border);
  white-space: nowrap;
  position: sticky; top: 0; z-index: 2;
}
tbody td, tfoot td, tfoot th {
  padding: 2px 6px;
  border: 1px solid var(--border);
  white-space: nowrap;
  overflow: hidden; text-overflow: ellipsis; max-width: 320px;
}
@media (min-width: 480px) {
  thead th, tbody td, tfoot td, tfoot th { padding: 3px 8px; }
}
tbody tr:hover td { background: color-mix(in oklab, var(--primary) 4%, transparent); }
.num, td.num, th.num {
  text-align: right;
  font-family: ui-monospace, "SF Mono", Menlo, Consolas, monospace;
  font-variant-numeric: tabular-nums;
  font-feature-settings: "tnum";
  white-space: nowrap;
}
.section-card td.num { white-space: nowrap; }

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
  box-shadow: none; }
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
.empty-state[hidden] { display: none; }
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

/* Badge — shadcn-style soft tones on tinted backgrounds. */
.chip {
  display: inline-block; padding: 0 8px;
  border-radius: 999px;
  font-size: var(--font-sm); font-weight: 500; line-height: 22px;
  border: 1px solid transparent;
  white-space: nowrap;
}
.chip-in       { color: var(--success); background: color-mix(in oklab, var(--success) 12%, transparent); border-color: color-mix(in oklab, var(--success) 30%, transparent); }
.chip-out      { color: var(--error); background: var(--error-bg); border-color: var(--error-border); }
.chip-transfer { color: var(--info); background: var(--info-bg); border-color: var(--info-border); }
.chip-neutral  { color: var(--text-secondary); background: var(--bg-fill); border-color: var(--border-secondary); }

/* Colored amounts — tabular figures everywhere money appears. */
.amt-in      { color: var(--success); font-weight: 500; font-variant-numeric: tabular-nums; font-feature-settings: "tnum"; }
.amt-out     { color: var(--text); font-weight: 500; font-variant-numeric: tabular-nums; font-feature-settings: "tnum"; }
.amt-neutral { color: var(--text-secondary); font-variant-numeric: tabular-nums; font-feature-settings: "tnum"; }

/* Sheet scroll regions: the wrapper scrolls, never the page. */
.table-wrap { width: 100%; max-width: 100%; overflow-x: auto; margin: 0 0 8px; -webkit-overflow-scrolling: touch; }
.table-wrap table { min-width: 560px; }

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

/* Pivot roll-forward report: filter checklists, segmented controls and
 * the grouped Akun | Kategori | Nama | Amount table. */
.pivot-checks { display: grid; gap: 2px; padding: 8px 16px 12px; }
.pivot-check {
  display: inline-flex; align-items: center; gap: 6px;
  padding: 4px 10px; font-size: 13px; color: var(--text-secondary);
  background: var(--bg-container);
  border: 1px solid var(--border); border-radius: 999px;
  cursor: pointer;
}
.pivot-checks .pivot-check {
  display: flex; width: 100%;
  align-items: center; gap: 10px;
  min-height: 44px; margin: 0; padding: 6px 0;
  font-size: var(--font-base); color: var(--text);
  background: transparent; border: 0; border-radius: 0;
}
.pivot-check input { width: auto; margin: 0; accent-color: var(--primary); }
.pivot-checks .pivot-check input { width: 16px; height: 16px; flex: none; }
.pivot-group { border-top: 1px solid var(--border-secondary); }
.pivot-group:first-of-type { border-top: 0; }
.pivot-group-head {
  display: flex; align-items: center; justify-content: space-between;
  gap: 12px; padding: 10px 16px 0;
}
.pivot-group-toggle { font-weight: 600; }
.pivot-group-toggle .pivot-caret { display: inline-block; transition: transform 0.15s; }
.pivot-group-toggle[aria-expanded="false"] .pivot-caret { transform: rotate(-90deg); }
.pivot-group-actions { font-size: var(--font-sm); white-space: nowrap; }
.pivot-akun-tag { color: var(--text-tertiary); font-size: 12px; }
.pivot-panel { display: grid; gap: 12px; padding: 12px 16px; }
.pivot-row { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; }
.pivot-seg { display: inline-flex; gap: 6px; flex-wrap: wrap; }
.pivot-seg button {
  width: auto; padding: 5px 12px;
  font-size: 12px; font-weight: 500; line-height: 1.5;
  color: var(--text-secondary); background: var(--bg-container);
  border: 1px solid var(--border-secondary); border-radius: 999px;
  box-shadow: none; cursor: pointer;
}
.pivot-seg button:hover { color: var(--primary); border-color: var(--primary); }
.pivot-seg button[aria-pressed="true"] {
  color: var(--primary); border-color: var(--primary); background: var(--primary-bg);
}
#pivot-q { flex: 1 1 160px; width: auto; max-width: 280px; }
.section-card .pivot-table thead th {
  background: var(--bg-fill);
  font-size: 11px;
  font-weight: 600; text-transform: none; letter-spacing: normal;
  color: var(--text-secondary);
  border: 1px solid var(--border);
  position: sticky; top: 0; z-index: 2;
}
.pivot-toggle-row { display: flex; justify-content: center; margin: 0 0 16px; }
#pivot-toggle {
  width: auto; display: inline-flex; align-items: center; gap: 6px;
  padding: 7px 14px; font-size: 13px; font-weight: 500; line-height: 1.5;
  color: var(--text-secondary); background: var(--bg-container);
  border: 1px solid var(--border); border-radius: var(--radius);
  box-shadow: var(--shadow-sm); cursor: pointer; text-decoration: none;
}
#pivot-toggle:hover { color: var(--primary); border-color: var(--primary); }
.pivot-table td.num, .pivot-table th.num { white-space: nowrap; }
/* Sheet scroll region: horizontal on narrow screens, vertical past
 * max-height; thead pins to region top, grand total pins to bottom. */
.pivot-wrap {
  overflow: auto;
  max-width: 100%;
  max-height: calc(100vh - 220px);
  border: 1px solid var(--border);
  background: var(--bg-container);
}
.pivot-wrap .pivot-table { border: 0; }
.pivot-table { font-size: 12px; }
.pivot-table td, .pivot-table th { padding: 2px 6px; border: 1px solid var(--border); white-space: nowrap; }
.pivot-sub td { font-weight: 700; cursor: pointer; border: 1px solid var(--border); background: var(--bg-fill); }
.pivot-sub:hover td { background: color-mix(in oklab, var(--primary) 8%, transparent); }
.pivot-sub:focus { outline: none; }
.pivot-sub:focus-visible { outline: 2px solid var(--primary); outline-offset: -2px; }
.pivot-caret { display: inline-block; width: 1.2em; color: var(--text-tertiary); font-weight: 400; }
.pivot-atotal td {
  font-weight: 700; border: 1px solid var(--border);
  background: color-mix(in oklab, var(--primary) 6%, transparent);
  font-variant-numeric: tabular-nums;
}
.pivot-grand td {
  font-weight: 700; border: 1px solid var(--border);
  position: sticky; bottom: 0; background: var(--bg-fill);
  font-variant-numeric: tabular-nums;
}
.pivot-neg { color: var(--error); font-weight: 500; }
.pivot-akun-cell { color: var(--text-secondary); }
.pivot-kat-link { color: var(--primary); text-decoration: none; font-weight: 500; }
.pivot-kat-link:visited { color: var(--primary); }
.pivot-kat-link:hover { color: var(--primary-hover); text-decoration: underline; }

/* Page-level overflow guard: wrappers scroll internally, the page never
 * scrolls sideways. overflow-x: clip (not hidden) preserves sticky
 * headers inside .table-wrap / .pivot-wrap / .nav. */
main { min-width: 0; max-width: 100%; overflow-x: clip; }
body.signed-in > main { min-width: 0; overflow-x: clip; }
.section-card { max-width: 100%; overflow-x: clip; }
.section-card .table-wrap, .section-card .pivot-wrap { max-width: 100%; }
/* Compact tap targets: 44px minimum on touch layouts. Desktop keeps the
 * denser 32px controls via the min-width queries below. */
@media (max-width: 767px) {
  button, input, select { min-height: 44px; }
  .sidebar a, .sidebar form button { min-height: 44px; }
  .pill, .txn-preset, .pivot-seg button, #pivot-toggle,
  .page-head-actions .pill, .balance-hero-actions .pill { min-height: 44px; }
  .topbar-icon { min-width: 44px; min-height: 44px; }
}

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
  display: flex; gap: 16px; align-items: baseline; margin: -8px -8px 16px;
  font-size: var(--font-base);
  padding: 0 8px;
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
  background: var(--primary); color: var(--primary-foreground);
  border-color: var(--primary);
  font-weight: 500;
}

:root {
  --home-gradient:  linear-gradient(135deg, #22C55E, #15803D);
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
  width: auto; padding: 7px 12px; font-size: 12px; font-weight: 600;
  color: var(--text); background: var(--bg-container);
  border: 1px solid var(--border); border-radius: var(--radius);
  text-decoration: none; white-space: nowrap;
  transition: border-color 0.15s, color 0.15s, background 0.15s;
}
.balance-hero-actions .pill:hover {
  color: var(--primary); border-color: var(--primary);
}
.balance-hero-actions .pill.primary {
  color: var(--primary-foreground); background: var(--primary); border-color: var(--primary);
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
  box-shadow: var(--shadow-sm);
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
  background: var(--bg-fill);
  font-size: 11px; font-weight: 600;
  text-transform: none; letter-spacing: normal;
  color: var(--text-secondary);
  padding: 3px 8px;
  border: 1px solid var(--border);
  white-space: nowrap;
}
.section-card tbody td { padding: 3px 8px; border: 1px solid var(--border); white-space: nowrap; }
.section-card tbody tr:first-child td { border-top: 1px solid var(--border); }
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
  letter-spacing: -0.025em;
}
@media (min-width: 480px) {
  .page-head h1 { font-size: var(--font-h2); line-height: 1.21; }
}
.page-head-actions { display: flex; gap: 8px; flex-wrap: wrap; }
.page-head-actions .pill {
  display: inline-flex; align-items: center; gap: 4px;
  width: auto; padding: 7px 12px; font-size: 12px; font-weight: 600;
  color: var(--text); background: var(--bg-container);
  border: 1px solid var(--border); border-radius: var(--radius);
  text-decoration: none; white-space: nowrap;
  transition: border-color 0.15s, color 0.15s, background 0.15s;
}
.page-head-actions .pill:hover { color: var(--primary); border-color: var(--primary); }
.page-head-actions .pill.primary {
  color: var(--primary-foreground); background: var(--primary); border-color: var(--primary);
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
  box-shadow: var(--shadow-sm);
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
.stat-card.in  .stat-amount { color: var(--success); }
.stat-card.out .stat-amount { color: var(--error); }
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
  background: var(--bg-fill);
  font-size: 11px; font-weight: 600;
  color: var(--text-secondary);
  padding: 3px 8px !important;
  border: 1px solid var(--border);
}
.day-strip .day-net { float: right; color: var(--text-secondary); }
.day-strip .day-net.in  { color: var(--success); }
.day-strip .day-net.out { color: var(--error); }
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
.txn-item-amount.in  { color: var(--success); }
.txn-item-amount.out { color: var(--error); }
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
<label for="sidebar-toggle" class="hamburger" role="button" tabindex="0" aria-label="Toggle navigation">` + iconMenu + `</label>
<a href="/" class="topbar-brand"><span class="logo">S</span><span>Shima</span></a>
<form class="topbar-search" action="/search" method="get" role="search">
<input id="topbar-q" name="q" type="search" placeholder="Search&hellip;" aria-label="Search" autocomplete="off" autocapitalize="off" autocorrect="off" spellcheck="false">
<kbd>&#8984;K</kbd>
</form>
<div class="topbar-actions">
<a href="/notifications" class="topbar-icon" aria-label="{{.UnreadCount}} notifications">` + iconBell + `<span class="badge">{{if .UnreadCount}}{{.UnreadCount}}{{end}}</span></a>
</div>
</header>
<label for="sidebar-toggle" class="sidebar-overlay" aria-hidden="true"></label>
<aside class="sidebar" aria-label="Primary">
<p class="sidebar-brand">Shima</p>
<nav class="nav-group">
<p class="nav-group-label">Overview</p>
<a href="/"{{if eq .Route "home"}} aria-current="page"{{end}}><span class="nav-ico">` + iconHome + `</span>Home</a>
</nav>
<nav class="nav-group">
<p class="nav-group-label">Money</p>
<a href="/transactions"{{if eq .Route "transactions"}} aria-current="page"{{end}}><span class="nav-ico">` + iconTransactions + `</span>Transactions</a>
<a href="/spending"{{if eq .Route "spending"}} aria-current="page"{{end}}><span class="nav-ico">` + iconSpending + `</span>Spending</a>
<a href="/recap"{{if eq .Route "recap"}} aria-current="page"{{end}}><span class="nav-ico">` + iconRecap + `</span>Rekap</a>
<a href="/pivot"{{if eq .Route "pivot"}} aria-current="page"{{end}}><span class="nav-ico">` + iconPivot + `</span>Pivot</a>
<a href="/accounts"{{if eq .Route "accounts"}} aria-current="page"{{end}}><span class="nav-ico">` + iconAccounts + `</span>Accounts</a>
<a href="/debts"{{if eq .Route "debts"}} aria-current="page"{{end}}><span class="nav-ico">` + iconDebts + `</span>Sisa Hutang</a>
</nav>
<nav class="nav-group">
<p class="nav-group-label">Planning</p>
<a href="/income-templates"{{if eq .Route "income"}} aria-current="page"{{end}}><span class="nav-ico">` + iconIncome + `</span>Income</a>
<a href="/pos"{{if eq .Route "pos"}} aria-current="page"{{end}}><span class="nav-ico">` + iconPos + `</span>Pos</a>
</nav>
<nav class="nav-group">
<p class="nav-group-label">Activity</p>
<a href="/notifications"{{if eq .Route "notifications"}} aria-current="page"{{end}}><span class="nav-ico">` + iconBell + `</span>Notifications<span class="badge" aria-label="{{.UnreadCount}} unread">{{if .UnreadCount}}{{.UnreadCount}}{{end}}</span></a>
</nav>
<div class="sidebar-end">
<a href="/settings"{{if eq .Route "settings"}} aria-current="page"{{end}}><span class="nav-ico">` + iconSettings + `</span>Settings</a>
<form method="post" action="/logout">
<button type="submit">` + iconLogout + `Sign out</button>
</form>
</div>
</aside>
{{end}}
<main{{if .Compact}} class="compact"{{else if .Wide}} class="wide"{{end}}>
{{if .SignedIn}}<nav class="breadcrumb" aria-label="Breadcrumb"><a href="/">Shima</a><span class="sep">/</span><span class="crumb-current">{{.Title}}</span></nav>{{end}}
`

const layoutClose = `
</main>
<script>
/* Progressive enhancement: focus the top-bar search on Cmd/Ctrl+K or "/".
 * The form still works without JS (Enter submits to /search). */
(function(){
  var input = document.getElementById('topbar-q');
  if (!input) return;
  document.addEventListener('keydown', function(e){
    if ((e.key === 'k' || e.key === 'K') && (e.metaKey || e.ctrlKey)) {
      e.preventDefault(); input.focus(); input.select();
    } else if (e.key === '/' && document.activeElement !== input) {
      var tag = (document.activeElement && document.activeElement.tagName) || '';
      if (!/^(INPUT|TEXTAREA|SELECT)$/.test(tag)) { e.preventDefault(); input.focus(); }
    }
  });
})();
</script>
</body>
</html>`

const searchBody = `<header class="page-head">
  <div>
    <p class="home-eyebrow">Search</p>
    <h1>{{if .Query}}Results for &ldquo;{{.Query}}&rdquo;{{else}}Search{{end}}</h1>
  </div>
</header>
<form method="get" action="/search" class="search-page-form" role="search">
  <input type="search" name="q" value="{{.Query}}" autofocus
    placeholder="Search accounts, pos, transactions, counterparties&hellip;"
    autocomplete="off" autocapitalize="off" autocorrect="off" spellcheck="false"
    aria-label="Search">
  <button type="submit" class="btn-ico">` + iconSearch + `Search</button>
</form>
{{if .Error}}<p class="alert" role="alert">{{.Error}}</p>{{end}}
{{if .Query}}
  {{if .Total}}
    {{if .Accounts}}<section class="section-card search-group">
      <div class="section-card-head"><span class="section-card-title">Accounts</span><span class="search-count">{{len .Accounts}}</span></div>
      {{range .Accounts}}<a class="search-result" href="/accounts">
        <span class="search-result-ico">` + iconAccounts + `</span>
        <span class="search-result-main"><span class="search-result-title">{{.Name}}</span></span>
        <span class="search-result-tag">Account</span>
      </a>{{end}}
    </section>{{end}}
    {{if .Pos}}<section class="section-card search-group">
      <div class="section-card-head"><span class="section-card-title">Pos</span><span class="search-count">{{len .Pos}}</span></div>
      {{range .Pos}}<a class="search-result" href="/pos/{{.ID}}">
        <span class="search-result-ico">` + iconPos + `</span>
        <span class="search-result-main"><span class="search-result-title">{{.Name}}</span>{{if .AccountName}}<span class="search-result-sub">{{.AccountName}}</span>{{end}}</span>
        <span class="search-result-tag">{{.Currency}}</span>
      </a>{{end}}
    </section>{{end}}
    {{if .Transactions}}<section class="section-card search-group">
      <div class="section-card-head"><span class="section-card-title">Transactions</span><span class="search-count">{{len .Transactions}}</span></div>
      {{range .Transactions}}<a class="search-result" href="{{if .PosID}}/pos/{{.PosID}}{{else}}/transactions{{end}}">
        <span class="search-result-ico">` + iconTransactions + `</span>
        <span class="search-result-main">
          <span class="search-result-title">{{.Label}}</span>
          <span class="search-result-sub">{{txnLabel .Type}}{{if .PosName}} &middot; {{.PosName}}{{end}}{{if .Date}} &middot; {{.Date}}{{end}}</span>
        </span>
        <span class="search-result-amt {{txnAmt .Type}}">{{txnSign .Type}}{{money .Amount .Currency}}</span>
      </a>{{end}}
    </section>{{end}}
    {{if .IncomeTemplates}}<section class="section-card search-group">
      <div class="section-card-head"><span class="section-card-title">Income templates</span><span class="search-count">{{len .IncomeTemplates}}</span></div>
      {{range .IncomeTemplates}}<a class="search-result" href="/income-templates/{{.ID}}">
        <span class="search-result-ico">` + iconIncome + `</span>
        <span class="search-result-main"><span class="search-result-title">{{.Name}}</span></span>
        <span class="search-result-tag">Income</span>
      </a>{{end}}
    </section>{{end}}
    {{if .Counterparties}}<section class="section-card search-group">
      <div class="section-card-head"><span class="section-card-title">Counterparties</span><span class="search-count">{{len .Counterparties}}</span></div>
      <div class="search-chips">{{range .Counterparties}}<span class="search-chip">{{.Name}}</span>{{end}}</div>
    </section>{{end}}
  {{else}}
    <div class="section-card">
      <div class="empty-state"><p class="empty-state-text">No matches for &ldquo;{{.Query}}&rdquo;.</p></div>
    </div>
  {{end}}
{{else}}
  <p class="subtitle">Type a term above to search across your accounts, pos, transactions, counterparties and income templates.</p>
{{end}}`

const loanLoginBody = `<div class="loan-brand"><span class="loan-logo">S</span> Shima Loans</div>
<h1>Loan access</h1>
<p class="subtitle">Sign in to view your loan balance and submit a payment.</p>
{{if .Error}}<p class="alert" role="alert">{{.Error}}</p>{{end}}
<form method="post" action="/loan/{{.PosID}}/login">
<div class="field">
<label for="username">Username</label>
<input id="username" name="username" autocomplete="username"
  autocapitalize="off" autocorrect="off" spellcheck="false" required autofocus>
</div>
<div class="field">
<label for="password">Password</label>
<input id="password" name="password" type="password" autocomplete="current-password" required>
</div>
<button type="submit" class="btn-ico">` + iconLogin + `Sign in</button>
</form>`

const loanViewBody = `<div class="loan-topline">
<div class="loan-brand"><span class="loan-logo">S</span> Shima Loans</div>
<form method="post" action="/loan/{{.PosID}}/logout"><button type="submit" class="linkbtn btn-ico">` + iconLogout + `Sign out</button></form>
</div>
<header class="page-head"><div><p class="home-eyebrow">Loan</p><h1>{{.LoanName}}</h1></div></header>
{{if .Flash}}<p class="flash" role="status">{{.Flash}}</p>{{end}}
{{if .Error}}<p class="alert" role="alert">{{.Error}}</p>{{end}}

<section class="loan-summary">
  <div class="loan-summary-head">
    <span class="loan-summary-label">Outstanding</span>
    <span class="loan-summary-amt">{{money .Outstanding .Currency}}</span>
  </div>
  <div class="loan-progress"><div class="loan-progress-bar" style="width:{{.PctRepaid}}%"></div></div>
  <div class="loan-summary-meta">{{money .Balance .Currency}} repaid of {{money .Target .Currency}} &middot; {{.PctRepaid}}%</div>
</section>

<section class="section-card">
  <div class="section-card-head"><span class="section-card-title">Submit a payment</span></div>
  <div class="loan-card-body">
    <form method="post" action="/loan/{{.PosID}}/payments">
      <div class="field"><label for="payer">Who paid</label>
        <input id="payer" name="payer_name" value="{{.BorrowerName}}" required></div>
      <div class="field"><label for="amount">Amount ({{.Currency}})</label>
        <input id="amount" name="amount" inputmode="numeric" autocomplete="off" required></div>
      <div class="field"><label for="date">Date</label>
        <input id="date" name="date" type="date" value="{{.TodayISO}}" required></div>
      <div class="field"><label for="note">Note (optional)</label>
        <input id="note" name="note" autocomplete="off"></div>
      <button type="submit" class="btn-ico">` + iconSend + `Submit for approval</button>
    </form>
  </div>
</section>

<section class="section-card">
  <div class="section-card-head"><span class="section-card-title">Your submissions</span></div>
  {{if .Submissions}}
    {{range .Submissions}}
    <div class="loan-sub">
      <div class="loan-sub-main">
        <span class="loan-sub-amt">{{money .Amount $.Currency}}</span>
        <span class="loan-sub-meta">{{.PayerName}}{{if .Date}} &middot; {{.Date}}{{end}}{{if .Note}} &middot; {{.Note}}{{end}}{{if .Reason}} &middot; {{.Reason}}{{end}}</span>
      </div>
      <span class="loan-status loan-status-{{.Status}}">{{.Status}}</span>
      {{if .CanCancel}}<form method="post" action="/loan/{{$.PosID}}/payments/{{.ID}}/cancel" class="loan-sub-cancel"><button type="submit" class="linkbtn btn-ico">` + iconX + `Cancel</button></form>{{end}}
    </div>
    {{end}}
  {{else}}<div class="loan-card-body"><p class="subtitle">No submissions yet.</p></div>{{end}}
</section>`

const loanSetupBody = `<header class="page-head"><div><p class="home-eyebrow">New</p><h1>Set up a loan</h1></div></header>
<p class="subtitle">Creates a loan Pos, funds &amp; disburses it, and sets the borrower login &mdash; in one step.</p>
{{range .Errors}}<p class="alert" role="alert">{{.}}</p>{{end}}
<form method="post" action="/loans/new">
<div class="field"><label for="name">Loan name</label>
  <input id="name" name="name" value="{{.Name}}" required autofocus></div>
<div class="field"><label for="borrower">Borrower name</label>
  <input id="borrower" name="borrower_name" value="{{.BorrowerName}}" required>
  <p class="hint">Counterparty on the disbursement, and the default payer on repayments.</p></div>
<div class="field"><label for="amount">Loan amount (IDR)</label>
  <input id="amount" name="amount" inputmode="numeric" value="{{.AmountRaw}}" required>
  <p class="hint">Becomes the loan target. Balance climbs from 0 to here as it's repaid.</p></div>
<div class="field"><label for="account">Cash account</label>
  <select id="account" name="account_id" required>
    <option value="">Choose an account&hellip;</option>
    {{range .Accounts}}<option value="{{.ID}}"{{if eq .ID $.AccountID}} selected{{end}}>{{.Name}}</option>{{end}}
  </select>
  <p class="hint">The account the cash flows through (funded in, disbursed out).</p></div>
<div class="field"><label for="funded">Funded from</label>
  <input id="funded" name="funded_from" value="{{.FundedFrom}}" placeholder="e.g. transfer from BCA, or a lender's name" required>
  <p class="hint">Counterparty for the funding entry &mdash; your transfer in, or who you borrowed from.</p></div>
<div class="field"><label for="lusername">Borrower username</label>
  <input id="lusername" name="username" value="{{.Username}}" autocapitalize="off" autocorrect="off" spellcheck="false" required></div>
<div class="field"><label for="lpassword">Borrower password</label>
  <input id="lpassword" name="password" type="password" required>
  <p class="hint">Share this with the borrower along with the loan link.</p></div>
<button type="submit" class="btn-ico">` + iconPlus + `Create loan</button>
</form>`

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
<button type="submit" class="btn-ico">` + iconLogin + `Sign in</button>
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
<button type="submit" class="btn-ico">` + iconCheck + `Verify</button>
</form>
<div class="aside">
<form method="post" action="/login">
<input type="hidden" name="identifier" value="{{.Identifier}}">
<button type="submit" class="linkbtn btn-ico">` + iconRefresh + `Send a new code</button>
</form>
&nbsp;·&nbsp;
<a class="linkbtn" href="/login">Use a different identifier</a>
</div>`

const notificationsBody = `<header class="page-head">
  <div>
    <p class="home-eyebrow">Activity</p>
    <h1>Notifications</h1>
  </div>
  {{if .UnreadCount}}<div class="page-head-actions">
    <form method="post" action="/notifications/mark-all-read">
      <button type="submit" class="pill btn-ico">` + iconCheckDouble + `Mark all read ({{.UnreadCount}})</button>
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
  <button type="submit" class="linkbtn btn-ico">` + iconCheck + `Mark read</button>
  </form>
  {{end}}
  </li>
  {{end}}
  </ul>
</section>
{{end}}`

const debtsBody = `<header class="page-head">
  <div>
    <p class="home-eyebrow">Money</p>
    <h1>Sisa Hutang</h1>
  </div>
</header>

{{if .LoadError}}
<p class="alert" role="alert">Couldn&rsquo;t load debts. Refresh in a moment.</p>
{{else if not .Items}}
<div class="section-card">
<div class="empty-state">
<svg viewBox="0 0 64 41" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
<ellipse cx="32" cy="33" rx="32" ry="7" fill="currentColor" opacity="0.08"/>
<rect x="14" y="6" width="36" height="24" rx="2" stroke="currentColor" stroke-width="1" fill="none" opacity="0.5"/>
<path d="M20 22h24" stroke="currentColor" stroke-width="2" opacity="0.5"/>
</svg>
<p class="empty-state-text">No open debts.</p>
<p class="empty-state-hint">Every inter-Pos borrow is fully repaid.</p>
</div>
</div>
{{else}}
<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Open obligations</span>
    <span class="section-card-meta">{{len .Items}} items</span>
  </div>
  <div class="table-wrap"><table>
    <thead><tr><th>Debtor &rarr; Creditor</th><th class="num">Outstanding</th><th class="num">Owed</th><th class="num">Repaid</th><th>Since</th></tr></thead>
    <tbody>
    {{range .Items}}<tr>
      <td>{{.DebtorPos}} &rarr; {{.CreditorPos}}</td>
      <td class="num"><strong>{{money .Outstanding .Currency}}</strong></td>
      <td class="num">{{money .Owed .Currency}}</td>
      <td class="num">{{money .Repaid .Currency}}</td>
      <td>{{relTime .CreatedAt}}</td>
    </tr>{{end}}
    </tbody>
  </table></div>
</section>
<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Totals outstanding</span>
  </div>
  <div class="table-wrap"><table>
    <tbody>
    {{range .Totals}}<tr>
      <td>{{.Currency}}</td>
      <td class="num"><strong>{{money .Outstanding .Currency}}</strong></td>
    </tr>{{end}}
    </tbody>
  </table></div>
</section>
{{end}}`

const recapBody = `<header class="page-head">
  <div>
    <p class="home-eyebrow">Analytics</p>
    <h1>Rekap</h1>
  </div>
</header>

<form method="get" action="/recap" class="txn-filter">
  <span class="txn-filter-label">Bucket</span>
  <span class="txn-presets">
    <a class="txn-preset{{if eq .Bucket "week"}} active{{end}}" href="/recap?bucket=week&amp;from={{.From}}&amp;to={{.To}}">Week</a>
    <a class="txn-preset{{if eq .Bucket "month"}} active{{end}}" href="/recap?bucket=month&amp;from={{.From}}&amp;to={{.To}}">Month</a>
    <a class="txn-preset{{if eq .Bucket "year"}} active{{end}}" href="/recap?bucket=year&amp;from={{.From}}&amp;to={{.To}}">Year</a>
  </span>
  <span class="txn-filter-dates">
    <input type="date" name="from" value="{{.From}}" aria-label="From">
    <span class="sep">&rarr;</span>
    <input type="date" name="to"   value="{{.To}}"   aria-label="To">
  </span>
  <input type="hidden" name="bucket" value="{{.Bucket}}">
  <button type="submit" class="btn-ico">` + iconFilter + `Filter</button>
</form>

{{range .Errors}}<p class="alert" role="alert">{{.}}</p>{{end}}

{{if .LoadError}}
<p class="alert" role="alert">Couldn&rsquo;t load recap. Refresh in a moment.</p>
{{else if not .Totals}}
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
<p class="empty-state-text">No movement in this range.</p>
<p class="empty-state-hint">Adjust the bucket or the dates.</p>
</div>
</div>
{{else}}
{{range $t := .Totals}}
<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">{{$t.Currency}} &middot; in {{money $t.Inflow $t.Currency}} &middot; out {{money $t.Outflow $t.Currency}}</span>
    <span class="section-card-meta">net {{money $t.Net $t.Currency}}</span>
  </div>
  <div class="table-wrap"><table>
    <thead><tr><th>Period</th><th class="num">In</th><th class="num">Out</th><th class="num">Net</th></tr></thead>
    <tbody>
    {{range $t.Rows}}<tr>
      <td>{{.Period}}</td>
      <td class="num amt-in">+{{money .Inflow $t.Currency}}</td>
      <td class="num">{{money .Outflow $t.Currency}}</td>
      <td class="num"><strong>{{money .Net $t.Currency}}</strong></td>
    </tr>{{end}}
    </tbody>
  </table></div>
</section>
{{end}}
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
  <button type="submit" class="btn-ico">` + iconFilter + `Filter</button>
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
  <td><a href="/pos/{{$col.PosID}}">{{$col.Name}}</a>{{if $col.AccountName}} <span class="quiet">· {{$col.AccountName}}</span>{{end}}</td>
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

const pivotBody = `<header class="page-head">
  <div>
    <p class="home-eyebrow">Analytics</p>
    <h1>Pivot Roll-forward</h1>
  </div>
  <div class="page-head-actions">
    <button type="button" class="linkbtn btn-ico" id="pivot-reset-top">` + iconReset + `Reset filters</button>
  </div>
</header>

<p class="subtitle" id="pivot-summary" role="status">Loading&hellip;</p>

{{if .LoadError}}
<p class="alert" role="alert">Some data could not be loaded. The view may be incomplete.</p>
{{end}}

<div class="pivot-toggle-row">
  <button type="button" id="pivot-toggle" aria-expanded="false" aria-controls="pivot-filters">` + iconSettings + `Show filters &#9662;</button>
</div>

<div id="pivot-filters" hidden>
<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Akun</span>
    <span class="section-card-meta">
      <button type="button" class="linkbtn btn-ico" data-akun-all>` + iconCheckSquare + `Select all</button>
      &middot;
      <button type="button" class="linkbtn btn-ico" data-akun-none>` + iconX + `Clear</button>
    </span>
  </div>
  <div class="pivot-checks" id="pivot-akun">
    {{range .Accounts}}<label class="pivot-check"><input type="checkbox" data-akun="{{.ID}}" checked> <span>{{.Name}}</span></label>{{else}}<p class="subtitle">No accounts.</p>{{end}}
  </div>
</section>

<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Kategori</span>
    <span class="section-card-meta">
      <button type="button" class="linkbtn btn-ico" data-kat-all>` + iconCheckSquare + `Select all</button>
      &middot;
      <button type="button" class="linkbtn btn-ico" data-kat-none>` + iconX + `Clear</button>
    </span>
  </div>
  {{range .Groups}}
  <div class="pivot-group" data-group="{{.AccountID}}">
    <div class="pivot-group-head">
      <button type="button" class="linkbtn pivot-group-toggle" data-fgroup="{{.AccountID}}" aria-expanded="true"><span class="pivot-caret">&#9662;</span>{{.AccountName}}</button>
      <span class="pivot-group-actions">
        <button type="button" class="linkbtn btn-ico" data-group-all="{{.AccountID}}">` + iconCheckSquare + `Select all</button>
        &middot;
        <button type="button" class="linkbtn btn-ico" data-group-none="{{.AccountID}}">` + iconX + `Clear</button>
      </span>
    </div>
    <div class="pivot-checks" data-fchecks="{{.AccountID}}">
      {{range .Positions}}<label class="pivot-check" data-kat-label="{{.AccountID}}"><input type="checkbox" data-kat="{{.PosID}}" data-akun="{{.AccountID}}" checked> <span>{{.Name}} <span class="pivot-akun-tag">&middot; {{.AccountName}}</span></span></label>{{end}}
    </div>
  </div>
  {{else}}<div class="pivot-checks"><p class="subtitle">No positions.</p></div>{{end}}
  <div class="pivot-checks" id="pivot-nokat" hidden><p class="subtitle">Select an Akun above to see its Kategori.</p></div>
</section>

<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Filters</span>
  </div>
  <div class="pivot-panel">
    <div class="pivot-row">
      <span class="txn-filter-label">Period</span>
      <span class="pivot-seg" id="pivot-preset" role="group" aria-label="Date range">
        <button type="button" data-preset="month">` + iconCalendar + `This month</button>
        <button type="button" data-preset="quarter">` + iconCalendar + `This quarter</button>
        <button type="button" data-preset="year">` + iconCalendar + `This year</button>
        <button type="button" data-preset="all" aria-pressed="true">` + iconClock + `All time</button>
      </span>
      <span class="txn-filter-dates">
        <input type="date" id="pivot-from" aria-label="From">
        <span class="sep">&rarr;</span>
        <input type="date" id="pivot-to" aria-label="To">
      </span>
    </div>
    <div class="pivot-row">
      <span class="txn-filter-label">Movement</span>
      <span class="pivot-seg" id="pivot-type" role="group" aria-label="Movement type">
        <button type="button" data-type="all" aria-pressed="true">` + iconLayers + `All</button>
        <button type="button" data-type="money_in">` + iconArrowDown + `Money in</button>
        <button type="button" data-type="money_out">` + iconArrowUp + `Money out</button>
      </span>
      <input type="search" id="pivot-q" placeholder="Search Nama&hellip;" aria-label="Search Nama" autocomplete="off">
    </div>
    <div class="pivot-row">
      <span class="txn-filter-label">View</span>
      <label class="pivot-check"><input type="checkbox" id="pivot-empty"> <span>Show empty positions</span></label>
      <label class="pivot-check"><input type="checkbox" id="pivot-akuntotal" checked> <span>Show Akun totals</span></label>
      <span class="pivot-seg" id="pivot-sort" role="group" aria-label="Sort within Kategori">
        <button type="button" data-sort="date" aria-pressed="true">` + iconCalendar + `By date</button>
        <button type="button" data-sort="amount">` + iconBanknote + `By amount</button>
      </span>
      <button type="button" class="linkbtn btn-ico" id="pivot-expand">` + iconChevDown + `Expand all</button>
      &middot;
      <button type="button" class="linkbtn btn-ico" id="pivot-collapse">` + iconChevUp + `Collapse all</button>
    </div>
  </div>
</section>
</div>

<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Report</span>
    <span class="section-card-meta" id="pivot-count"></span>
  </div>
  <div class="table-wrap pivot-wrap"><table class="pivot-table">
    <thead><tr><th>Akun</th><th>Kategori</th><th>Nama</th><th class="num">Amount</th></tr></thead>
    <tbody id="pivot-body"></tbody>
    <tfoot id="pivot-foot"></tfoot>
  </table></div>
  <div class="empty-state" id="pivot-empty-state" hidden>
    <svg viewBox="0 0 64 41" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
      <ellipse cx="32" cy="33" rx="32" ry="7" fill="currentColor" opacity="0.08"/>
      <rect x="14" y="6" width="36" height="24" rx="2" stroke="currentColor" stroke-width="1" fill="none" opacity="0.5"/>
      <rect x="20" y="20" width="4" height="6" fill="currentColor" opacity="0.25"/>
      <rect x="28" y="14" width="4" height="12" fill="currentColor" opacity="0.25"/>
      <rect x="36" y="10" width="4" height="16" fill="currentColor" opacity="0.25"/>
      <rect x="44" y="22" width="4" height="4" fill="currentColor" opacity="0.25"/>
    </svg>
    <p class="empty-state-text">No movements match the current filters.</p>
    <p class="empty-state-action"><button type="button" class="linkbtn btn-ico" id="pivot-reset-empty">` + iconReset + `Reset filters</button></p>
  </div>
</section>

<script type="application/json" id="pivot-data">{{.DataJSON}}</script>
<script type="application/json" id="pivot-saved">{{.SavedFiltersJSON}}</script>
<script>
(function(){
  var el = document.getElementById('pivot-data');
  var data = {accounts: [], positions: [], movements: []};
  try { data = Object.assign(data, JSON.parse(el.textContent)); } catch (e) {}
  var savedQ = '';
  try { savedQ = JSON.parse(document.getElementById('pivot-saved').textContent || '""'); } catch (e) {}
  var savedParams = new URLSearchParams(savedQ);
  var knownAkun = {};
  var knownKat = {};
  data.accounts.forEach(function(a){ knownAkun[a.ID] = true; });
  data.positions.forEach(function(p){ knownKat[p.PosID] = true; });

  var akun = {};
  var kat = {};
  Object.keys(knownAkun).forEach(function(id){ akun[id] = true; });
  Object.keys(knownKat).forEach(function(id){ kat[id] = true; });
  var state = {preset: 'all', from: '', to: '', type: 'all', q: '',
    showEmpty: false, showAkunTotal: true, sort: 'date', collapsed: {}, showFilters: false, fgroups: {}};
  var lastVisibleKats = [];

  function $(s){ return document.querySelector(s); }
  function $all(s){ return Array.prototype.slice.call(document.querySelectorAll(s)); }

  function parseURL(){
    var p = new URLSearchParams(location.search);
    // Fresh visit (no filter params in the URL) restores the user's saved
    // view; explicit URL params (shared links) always win for the current
    // view. Panel chrome (filters, fgroups) is never restored — it always
    // follows the URL.
    var hasFilters = ['akun','kat','preset','from','to','type','q','empty','akuntotal','sort','collapsed']
      .some(function(k){ return p.has(k); });
    var src = hasFilters ? p : savedParams;
    if (src.has('akun')) {
      akun = {};
      src.get('akun').split(',').forEach(function(id){ if (knownAkun[id]) akun[id] = true; });
    }
    if (src.has('kat')) {
      kat = {};
      src.get('kat').split(',').forEach(function(id){ if (knownKat[id]) kat[id] = true; });
    }
    var preset = src.get('preset');
    if (preset === 'month' || preset === 'quarter' || preset === 'year' ||
        preset === 'custom' || preset === 'all') state.preset = preset;
    state.from = src.get('from') || '';
    state.to = src.get('to') || '';
    var type = src.get('type');
    if (type === 'money_in' || type === 'money_out') state.type = type;
    state.q = src.get('q') || '';
    state.showEmpty = src.get('empty') === '1';
    state.showAkunTotal = src.get('akuntotal') !== '0';
    if (src.get('sort') === 'amount') state.sort = 'amount';
    state.showFilters = p.get('filters') === '1';
    state.fgroups = {};
    (p.get('fgroups') || '').split(',').forEach(function(id){
      if (knownAkun[id]) state.fgroups[id] = true;
    });
    state.collapsed = {};
    if (src.has('collapsed')) {
      (src.get('collapsed') || '').split(',').forEach(function(id){
        if (id && knownKat[id]) state.collapsed[id] = true;
      });
    } else {
      // Default: everything collapsed (totals only). An explicit
      // ?collapsed= (empty) means "expand all"; a list collapses just those.
      Object.keys(knownKat).forEach(function(id){ state.collapsed[id] = true; });
    }
  }

  function pad(n){ return (n < 10 ? '0' : '') + n; }
  function iso(d){ return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()); }
  function bounds(){
    var now = new Date();
    if (state.preset === 'month') {
      return {from: iso(new Date(now.getFullYear(), now.getMonth(), 1)), to: iso(now)};
    }
    if (state.preset === 'quarter') {
      return {from: iso(new Date(now.getFullYear(), Math.floor(now.getMonth() / 3) * 3, 1)), to: iso(now)};
    }
    if (state.preset === 'year') {
      return {from: iso(new Date(now.getFullYear(), 0, 1)), to: iso(now)};
    }
    if (state.preset === 'custom') {
      return {from: state.from, to: state.to};
    }
    return {from: '', to: ''};
  }

  var MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
  function mlabel(s){
    if (!s) return '';
    var parts = s.split('-');
    if (parts.length < 2) return s;
    return MONTHS[parseInt(parts[1], 10) - 1] + ' ' + parts[0];
  }

  function fmt(n){
    if (!n) return '0';
    var neg = n < 0;
    var s = String(Math.abs(n));
    var out = '';
    while (s.length > 3) { out = '.' + s.slice(-3) + out; s = s.slice(0, -3); }
    return (neg ? '-' : '') + s + out;
  }

  function esc(s){
    return String(s == null ? '' : s).replace(/[&<>"']/g, function(c){
      return {'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'}[c];
    });
  }

  function filteredRows(){
    var b = bounds();
    var q = state.q.trim().toLowerCase();
    return data.movements.filter(function(m){
      if (!akun[m.AccountID]) return false;
      if (!kat[m.PosID]) return false;
      if (b.from && m.Date < b.from) return false;
      if (b.to && m.Date > b.to) return false;
      if (state.type !== 'all' && m.Type !== state.type) return false;
      if (q && (m.Nama || '').toLowerCase().indexOf(q) < 0) return false;
      return true;
    });
  }

  function build(rows){
    var akuns = {};
    var order = [];
    function getAkun(id, name){
      var a = akuns[id];
      if (!a) { a = akuns[id] = {id: id, name: name, kats: {}, korder: []}; order.push(a); }
      return a;
    }
    rows.forEach(function(m){
      var a = getAkun(m.AccountID, m.AccountName);
      var k = a.kats[m.PosID];
      if (!k) { k = a.kats[m.PosID] = {id: m.PosID, name: m.PosName, rows: [], sub: 0}; a.korder.push(k); }
      k.rows.push(m);
      k.sub += m.Amount;
    });
    if (state.showEmpty) {
      data.positions.forEach(function(p){
        if (!akun[p.AccountID] || !kat[p.PosID]) return;
        var a = getAkun(p.AccountID, p.AccountName);
        if (!a.kats[p.PosID]) {
          var k = a.kats[p.PosID] = {id: p.PosID, name: p.Name, rows: [], sub: 0};
          a.korder.push(k);
        }
      });
    }
    order.sort(function(a, b){ return a.name < b.name ? -1 : a.name > b.name ? 1 : 0; });
    var grand = 0;
    order.forEach(function(a){
      a.korder.sort(function(x, y){ return x.name < y.name ? -1 : x.name > y.name ? 1 : 0; });
      a.total = 0;
      a.korder.forEach(function(k){
        if (state.sort === 'amount') {
          k.rows.sort(function(x, y){ return y.Amount - x.Amount || (x.Date < y.Date ? 1 : -1); });
        } else {
          k.rows.sort(function(x, y){ return x.Date < y.Date ? 1 : x.Date > y.Date ? -1 : 0; });
        }
        a.total += k.sub;
      });
      grand += a.total;
    });
    return {akuns: order, grand: grand};
  }

  function currentQuery(){
    var p = new URLSearchParams();
    var akunIds = Object.keys(akun).filter(function(id){ return akun[id]; });
    if (akunIds.length !== data.accounts.length) p.set('akun', akunIds.join(','));
    var katIds = Object.keys(kat).filter(function(id){ return kat[id]; });
    if (katIds.length !== data.positions.length) p.set('kat', katIds.join(','));
    if (state.preset !== 'all') p.set('preset', state.preset);
    if (state.preset === 'custom') {
      if (state.from) p.set('from', state.from);
      if (state.to) p.set('to', state.to);
    }
    if (state.type !== 'all') p.set('type', state.type);
    if (state.q.trim()) p.set('q', state.q.trim());
    if (state.showEmpty) p.set('empty', '1');
    if (!state.showAkunTotal) p.set('akuntotal', '0');
    if (state.sort !== 'date') p.set('sort', state.sort);
    if (state.showFilters) p.set('filters', '1');
    var fg = Object.keys(state.fgroups).filter(function(id){ return state.fgroups[id]; });
    if (fg.length) p.set('fgroups', fg.join(','));
    var c = Object.keys(state.collapsed).filter(function(id){ return state.collapsed[id]; });
    var allIds = Object.keys(knownKat);
    var allCollapsed = allIds.length > 0 && allIds.every(function(id){ return !!state.collapsed[id]; });
    if (allCollapsed) {
      p.delete('collapsed'); // default state: totals only, clean URL
    } else if (c.length || allIds.length) {
      p.set('collapsed', c.join(',')); // empty value = explicitly expanded all
    } else {
      p.delete('collapsed');
    }
    return p.toString();
  }

  function syncURL(){
    var s = currentQuery();
    history.replaceState(null, '', location.pathname + (s ? '?' + s : ''));
  }

  // Persist the filter view per user so a fresh visit (new session, no
  // URL params) restores it. Debounced; skips the initial render so
  // loading a shared link never clobbers the user's saved view until
  // they actually change something. Server no-ops without a DB.
  var lastSaved = null;
  var saveTimer = null;
  var hydrated = false;
  function scheduleSave(){
    if (saveTimer) return;
    saveTimer = setTimeout(function(){
      saveTimer = null;
      var s = currentQuery();
      if (s === lastSaved) return;
      lastSaved = s;
      try {
        fetch('/pivot/filters', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({filters: s})
        }).catch(function(){});
      } catch (e) {}
    }, 500);
  }

  function paintControls(){
    $all('#pivot-akun input[data-akun]').forEach(function(cb){ cb.checked = !!akun[cb.getAttribute('data-akun')]; });
    $all('input[data-kat]').forEach(function(cb){ cb.checked = !!kat[cb.getAttribute('data-kat')]; });
    $all('#pivot-preset button').forEach(function(b){
      b.setAttribute('aria-pressed', b.getAttribute('data-preset') === state.preset ? 'true' : 'false');
    });
    $all('#pivot-type button').forEach(function(b){
      b.setAttribute('aria-pressed', b.getAttribute('data-type') === state.type ? 'true' : 'false');
    });
    $all('#pivot-sort button').forEach(function(b){
      b.setAttribute('aria-pressed', b.getAttribute('data-sort') === state.sort ? 'true' : 'false');
    });
    var b = bounds();
    $('#pivot-from').value = b.from || state.from;
    $('#pivot-to').value = b.to || state.to;
    if (document.activeElement !== $('#pivot-q')) $('#pivot-q').value = state.q;
    $('#pivot-empty').checked = state.showEmpty;
    $('#pivot-akuntotal').checked = state.showAkunTotal;
    var fw = $('#pivot-filters');
    if (fw) fw.hidden = !state.showFilters;
    var tg = $('#pivot-toggle');
    if (tg) {
      tg.textContent = state.showFilters ? 'Hide filters ▴' : 'Show filters ▾';
      tg.setAttribute('aria-expanded', state.showFilters ? 'true' : 'false');
    }
    $all('[data-fgroup]').forEach(function(btn){
      var open = !state.fgroups[btn.getAttribute('data-fgroup')];
      btn.setAttribute('aria-expanded', open ? 'true' : 'false');
    });
    $all('[data-fchecks]').forEach(function(div){
      div.hidden = !!state.fgroups[div.getAttribute('data-fchecks')];
    });
    paintKatVisibility();
  }

  function paintKatVisibility(){
    var anyVisible = false;
    $all('[data-group]').forEach(function(div){
      var visible = !!akun[div.getAttribute('data-group')];
      div.hidden = !visible;
      if (visible) anyVisible = true;
    });
    var hint = $('#pivot-nokat');
    if (hint) hint.hidden = anyVisible || !data.accounts.length;
  }

  function render(){
    var rep = build(filteredRows());
    var body = $('#pivot-body');
    var foot = $('#pivot-foot');
    var html = '';
    var nMov = 0;
    lastVisibleKats = [];
    rep.akuns.forEach(function(a){
      a.korder.forEach(function(k){ nMov += k.rows.length; });
    });
    rep.akuns.forEach(function(a){
      var first = true;
      a.korder.forEach(function(k){
        lastVisibleKats.push(k.id);
        var collapsed = !!state.collapsed[k.id];
        var caret = collapsed ? '&#9656;' : '&#9662;';
        var expanded = collapsed ? 'false' : 'true';
        if (!collapsed) {
          k.rows.forEach(function(m){
            var cls = m.Amount < 0 ? ' class="num pivot-neg"' : ' class="num"';
            html += '<tr><td class="pivot-akun-cell">' + (first ? esc(a.name) : '') + '</td>' +
              '<td><a href="/pos/' + esc(m.PosID) + '" class="pivot-kat-link">' + esc(m.PosName) + '</a></td>' +
              '<td title="' + esc(m.Date) + '">' + esc(m.Nama) + '</td>' +
              '<td' + cls + '>' + fmt(m.Amount) + '</td></tr>';
            first = false;
          });
        }
        var akunCell = first ? esc(a.name) : '';
        first = false;
        var subCls = k.sub < 0 ? ' class="num pivot-neg"' : ' class="num"';
        html += '<tr class="pivot-sub" data-pos="' + esc(k.id) + '" tabindex="0" role="button" aria-expanded="' + expanded + '" title="Collapse/expand this Kategori\u2019s Nama rows">' +
          '<td class="pivot-akun-cell">' + akunCell + '</td><td><span class="pivot-caret">' + caret + '</span>' + esc(k.name) + ' Total</td><td></td>' +
          '<td' + subCls + '>' + fmt(k.sub) + '</td></tr>';
      });
      if (state.showAkunTotal) {
        var aCls = a.total < 0 ? ' class="num pivot-neg"' : ' class="num"';
        html += '<tr class="pivot-atotal"><td colspan="3">' + esc(a.name) + ' Total</td><td' + aCls + '>' + fmt(a.total) + '</td></tr>';
      }
    });
    body.innerHTML = html;
    var gCls = rep.grand < 0 ? ' class="num pivot-neg"' : ' class="num"';
    foot.innerHTML = '<tr class="pivot-grand"><td colspan="3">Grand Total</td><td' + gCls + '>' + fmt(rep.grand) + '</td></tr>';

    var selAkun = Object.keys(akun).filter(function(id){ return akun[id]; }).length;
    var selKat = Object.keys(kat).filter(function(id){ return kat[id]; }).length;
    var b = bounds();
    var range = 'All time';
    if (b.from || b.to) range = (b.from ? mlabel(b.from) : '…') + ' – ' + (b.to ? mlabel(b.to) : '…');
    $('#pivot-summary').textContent =
      selAkun + ' of ' + data.accounts.length + ' Akun · ' +
      selKat + ' of ' + data.positions.length + ' Kategori · ' + range;
    $('#pivot-count').textContent = nMov + ' movements · ' +
      (state.sort === 'amount' ? 'sorted by amount' : 'newest first');

    var empty = nMov === 0;
    document.querySelector('.pivot-wrap').hidden = empty;
    $('#pivot-empty-state').hidden = !empty;
    syncURL();
    // Record the state only after the first (hydrating) render, so a
    // shared link or a restored view never rewrites the saved settings
    // before the user changes anything.
    if (hydrated) scheduleSave();
    hydrated = true;
  }

  function refresh(){ paintKatVisibility(); render(); }

  $all('#pivot-akun input[data-akun]').forEach(function(cb){
    cb.addEventListener('change', function(){
      akun[cb.getAttribute('data-akun')] = cb.checked;
      refresh();
    });
  });
  $all('input[data-kat]').forEach(function(cb){
    cb.addEventListener('change', function(){
      kat[cb.getAttribute('data-kat')] = cb.checked;
      render();
    });
  });

  document.querySelector('[data-akun-all]').addEventListener('click', function(){
    Object.keys(knownAkun).forEach(function(id){ akun[id] = true; });
    paintControls(); refresh();
  });
  document.querySelector('[data-akun-none]').addEventListener('click', function(){
    akun = {};
    paintControls(); refresh();
  });
  document.querySelector('[data-kat-all]').addEventListener('click', function(){
    Object.keys(knownKat).forEach(function(id){ kat[id] = true; });
    paintControls(); render();
  });
  document.querySelector('[data-kat-none]').addEventListener('click', function(){
    kat = {};
    paintControls(); render();
  });
  $all('[data-group-all]').forEach(function(btn){
    btn.addEventListener('click', function(){
      var aid = btn.getAttribute('data-group-all');
      data.positions.forEach(function(p){ if (p.AccountID === aid) kat[p.PosID] = true; });
      paintControls(); render();
    });
  });
  $all('[data-group-none]').forEach(function(btn){
    btn.addEventListener('click', function(){
      var aid = btn.getAttribute('data-group-none');
      data.positions.forEach(function(p){ if (p.AccountID === aid) delete kat[p.PosID]; });
      paintControls(); render();
    });
  });
  $all('[data-fgroup]').forEach(function(btn){
    btn.addEventListener('click', function(){
      var aid = btn.getAttribute('data-fgroup');
      if (state.fgroups[aid]) delete state.fgroups[aid];
      else state.fgroups[aid] = true;
      paintControls(); syncURL();
    });
  });

  $all('#pivot-preset button').forEach(function(btn){
    btn.addEventListener('click', function(){
      state.preset = btn.getAttribute('data-preset');
      if (state.preset !== 'custom') { state.from = ''; state.to = ''; }
      paintControls(); render();
    });
  });
  $('#pivot-from').addEventListener('change', function(e){
    state.preset = 'custom'; state.from = e.target.value; paintControls(); render();
  });
  $('#pivot-to').addEventListener('change', function(e){
    state.preset = 'custom'; state.to = e.target.value; paintControls(); render();
  });

  $all('#pivot-type button').forEach(function(btn){
    btn.addEventListener('click', function(){
      state.type = btn.getAttribute('data-type');
      paintControls(); render();
    });
  });
  $('#pivot-q').addEventListener('input', function(e){
    state.q = e.target.value;
    render();
  });
  $('#pivot-empty').addEventListener('change', function(e){
    state.showEmpty = e.target.checked;
    render();
  });
  $('#pivot-akuntotal').addEventListener('change', function(e){
    state.showAkunTotal = e.target.checked;
    render();
  });
  $all('#pivot-sort button').forEach(function(btn){
    btn.addEventListener('click', function(){
      state.sort = btn.getAttribute('data-sort');
      paintControls(); render();
    });
  });
  $('#pivot-expand').addEventListener('click', function(){
    state.collapsed = {};
    render();
  });
  $('#pivot-collapse').addEventListener('click', function(){
    state.collapsed = {};
    lastVisibleKats.forEach(function(id){ state.collapsed[id] = true; });
    render();
  });

  function resetState(){
    akun = {};
    Object.keys(knownAkun).forEach(function(id){ akun[id] = true; });
    kat = {};
    Object.keys(knownKat).forEach(function(id){ kat[id] = true; });
    var _collapsed = {};
    Object.keys(knownKat).forEach(function(id){ _collapsed[id] = true; });
    state = {preset: 'all', from: '', to: '', type: 'all', q: '',
      showEmpty: false, showAkunTotal: true, sort: 'date', collapsed: _collapsed, showFilters: false, fgroups: {}};
    $('#pivot-q').value = '';
    paintControls(); render();
  }
  $('#pivot-toggle').addEventListener('click', function(){
    state.showFilters = !state.showFilters;
    paintControls(); syncURL();
  });
  $('#pivot-reset-top').addEventListener('click', resetState);
  $('#pivot-reset-empty').addEventListener('click', resetState);

  $('#pivot-body').addEventListener('click', function(e){
    var tr = e.target.closest('tr.pivot-sub');
    if (!tr || e.target.closest('a')) return;
    var id = tr.getAttribute('data-pos');
    if (state.collapsed[id]) delete state.collapsed[id];
    else state.collapsed[id] = true;
    render();
  });
  $('#pivot-body').addEventListener('keydown', function(e){
    if (e.key !== 'Enter' && e.key !== ' ') return;
    var tr = e.target.closest('tr.pivot-sub');
    if (!tr) return;
    e.preventDefault();
    var id = tr.getAttribute('data-pos');
    if (state.collapsed[id]) delete state.collapsed[id];
    else state.collapsed[id] = true;
    render();
  });

  parseURL();
  paintControls();
  render();
})();</script>`

const posBody = `{{if .NotFound}}
<h1>Pos not found</h1>
<p class="subtitle">No Pos with that id, or it has been removed.</p>
{{else}}
<h1>{{.Name}}{{if .Archived}} <span class="badge-rev">archived</span>{{end}}</h1>
<p class="subtitle">{{.Currency}}{{if .HasTarget}} &middot; target {{money .Target .Currency}}{{end}}</p>

{{if .LoadError}}
<p class="alert" role="alert">Some data could not be loaded. The view may be incomplete.</p>
{{end}}

<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Balance</span>
    <span class="section-card-meta">{{.Currency}}</span>
  </div>
  <div class="table-wrap">
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
  </div>
</section>

{{if .IsLoan}}
<section class="section-card">
  <div class="section-card-head"><span class="section-card-title">Loan</span></div>
  <div class="card-pad">
  {{if .Flash}}<p class="flash" role="status">{{.Flash}}</p>{{end}}
  {{if .Error}}<p class="alert" role="alert">{{.Error}}</p>{{end}}
  <div class="loan-summary">
    <div class="loan-summary-head">
      <span class="loan-summary-label">Outstanding</span>
      <span class="loan-summary-amt">{{money .LoanOutstanding .Currency}}</span>
    </div>
    <div class="loan-progress"><div class="loan-progress-bar" style="width:{{.LoanPctRepaid}}%"></div></div>
    <div class="loan-summary-meta">{{money .LoanRepaid .Currency}} repaid of {{money .Target .Currency}} &middot; {{.LoanPctRepaid}}%</div>
  </div>
  <p class="subtitle">Borrower portal: <a href="{{.BorrowerLoginURL}}">{{.BorrowerLoginURL}}</a>{{if .HasBorrowerLogin}} &middot; username <strong>{{.BorrowerUsername}}</strong>{{else}} &middot; <em>no borrower login set yet</em>{{end}}</p>
  </div>
</section>

<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Repayment submissions</span>
    {{if .LoanPendingCount}}<span class="section-card-meta">{{.LoanPendingCount}} pending</span>{{end}}
  </div>
  {{if .LoanSubmissions}}
  <div class="table-wrap">
  <table>
<thead><tr><th>Payer</th><th class="num">Amount</th><th>Date</th><th>Status</th><th></th></tr></thead>
<tbody>
{{range .LoanSubmissions}}
<tr>
<td>{{.PayerName}}{{if .Note}}<br><span class="hint">{{.Note}}</span>{{end}}</td>
<td class="num">{{money .Amount $.Currency}}</td>
<td>{{.Date}}</td>
<td><span class="loan-status loan-status-{{.Status}}">{{.Status}}</span></td>
<td class="num">
{{if eq .Status "pending"}}
<form method="post" action="/loan-submissions/{{.ID}}/approve" class="inline-form"><button type="submit" class="pill btn-ico">` + iconCheck + `Approve</button></form>
<form method="post" action="/loan-submissions/{{.ID}}/reject" class="inline-form"><button type="submit" class="linkbtn btn-ico">` + iconX + `Reject</button></form>
{{end}}
</td>
</tr>
{{end}}
</tbody>
</table>
  </div>
  {{else}}<div class="card-pad"><p class="subtitle">No repayment submissions yet.</p></div>{{end}}
</section>
{{end}}

<section class="section-card">
  <div class="section-card-head"><span class="section-card-title">Funding account</span></div>
  <div class="card-pad">
  {{if .AccountFlash}}<p class="flash" role="status">{{.AccountFlash}}</p>{{end}}
  <p class="subtitle">Currently held in <strong>{{.AccountName}}</strong>. Reassigning has snapshot semantics: per-account balances update retroactively, but this Pos's history is preserved.</p>
  <form method="post" action="/pos/{{.ID}}/account">
  <div class="field">
  <label for="change_account_id">Move to</label>
  <select id="change_account_id" name="account_id" required>
  {{range .Accounts}}<option value="{{.ID}}" {{if eq $.AccountID .ID}}selected{{end}}>{{.Name}}</option>
  {{end}}</select>
  </div>
  <button type="submit" class="btn-ico">` + iconCheck + `Save</button>
  </form>
  </div>
</section>

{{if not .Archived}}
<section class="section-card">
  <div class="section-card-head"><span class="section-card-title">Edit</span></div>
  <div class="card-pad">
  <form method="post" action="/pos/{{.ID}}/rename">
  <div class="field">
  <label for="rename_name">Name</label>
  <input id="rename_name" name="name" type="text" value="{{.Name}}" required maxlength="80">
  </div>
  <div class="field">
  <label for="rename_target">Target <span class="quiet">(optional, smallest unit; leave blank to clear)</span></label>
  <input id="rename_target" name="target" type="text" inputmode="numeric" pattern="[0-9.,]*" maxlength="16" value="{{if .HasTarget}}{{.Target}}{{end}}">
  </div>
  <p class="hint">Currency ({{.Currency}}) is fixed; archive and recreate this Pos to change it.</p>
  <button type="submit" class="btn-ico">` + iconCheck + `Save changes</button>
  </form>
  </div>
</section>

<section class="section-card">
  <div class="section-card-head"><span class="section-card-title">Archive</span></div>
  <div class="card-pad">
  <p class="subtitle">Archived Pos disappear from the home view but their history is preserved. Re-create or unarchive via SQL if needed.</p>
  <form method="post" action="/pos/{{.ID}}/archive" onsubmit="return confirm('Archive this Pos? It will hide from default views but past transactions stay.');">
  <button type="submit" class="btn-ico">` + iconArchive + `Archive this Pos</button>
  </form>
  </div>
</section>
{{end}}

{{if .Obligations}}
<section class="section-card">
  <div class="section-card-head"><span class="section-card-title">Open obligations</span></div>
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
<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Transactions</span>
    <span class="section-card-meta">{{len .Transactions}} items</span>
  </div>
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
    <a class="pill btn-ico" href="/loans/new">` + iconPlus + `Set up a loan</a>
    <a class="pill primary btn-ico" href="/pos/new">` + iconPlus + `New Pos</a>
  </div>
</header>

{{if .Flash}}<p class="flash" role="status">{{.Flash}}</p>{{end}}
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
    <form method="post" action="/pos/{{.ID}}/account" class="inline-row-form">
      <input type="hidden" name="back" value="list">
      <select name="account_id" required>
        {{$cur := .AccountID}}
        {{range $.Accounts}}<option value="{{.ID}}"{{if eq .ID $cur}} selected{{end}}>{{.Name}}</option>{{end}}
      </select>
      <button type="submit" class="btn-ico btn-sm btn-outline">` + iconCheck + `Save</button>
    </form>
  </td>
  <td>{{if .Archived}}<span class="chip">archived</span>{{else}}<span class="chip chip-in">active</span>{{end}}</td>
  <td><div class="inline-row-actions">
  {{if not .Archived}}
    <form method="post" action="/pos/{{.ID}}/archive" onsubmit="return confirm('Archive this Pos? It will be hidden from default lists.');" class="inline-form">
      <button type="submit" class="btn-sm btn-outline btn-ico">` + iconArchive + `Archive</button>
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
<ul>
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
<label for="target">Target <span class="quiet">(optional)</span></label>
<input id="target" name="target" type="text" inputmode="numeric"
  value="{{.TargetRaw}}"
  autocapitalize="off" autocorrect="off" spellcheck="false"
  pattern="[0-9.,]*" maxlength="16"
  placeholder="e.g. 12.000.000">
<p class="hint">Whole number in the smallest unit (rupiah for IDR, cents for USD). Leave blank for an open-ended Pos.</p>
</div>
<button type="submit" class="btn-ico">` + iconPlus + `Create Pos</button>
</form>
<p class="aside"><a class="linkbtn" href="/">&larr; Cancel</a></p>`

const transactionsBody = `<header class="page-head">
  <div>
    <p class="home-eyebrow">Money &middot; ledger</p>
    <h1>Transactions</h1>
  </div>
  <div class="page-head-actions">
    <a class="pill primary btn-ico" href="/transactions/new?type=money_in">` + iconPlus + `Income</a>
    <a class="pill btn-ico" href="/transactions/new?type=money_out">` + iconMinus + `Spending</a>
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
  <button type="submit" class="btn-ico">` + iconFilter + `Filter</button>
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
<ul>
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
{{range .PosOptions}}{{if .AccountName}}<option value="{{.Name}} ({{.AccountName}})"></option>{{else}}<option value="{{.Name}}"></option>{{end}}{{end}}
</datalist>
<p class="hint">Start typing to filter. Pick a suggestion or type the exact Pos name. Same name may appear under several accounts — pick the one with the right account in brackets. IDR only here; cross-currency stays behind the API.</p>
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
<label for="note">Note <span class="quiet">(optional)</span></label>
<input id="note" name="note" type="text" maxlength="200" value="{{.Note}}">
</div>
<button type="submit" class="btn-ico">{{if .IsIncoming}}` + iconPlus + `Record income{{else}}` + iconMinus + `Record spending{{end}}</button>
</form>
{{if and .IsIncoming .Templates}}
<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Or monthly income in one go</span>
    <span class="section-card-meta">{{len .Templates}} templates</span>
  </div>
  <ul class="notifs">
  {{range .Templates}}<li class="notif"><a class="notif-link" href="/income-templates/{{.ID}}"><strong>{{.Name}}</strong><span class="notif-body">Preview the split, enter the amount, apply once.</span></a></li>{{end}}
  </ul>
</section>
{{end}}
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
<p class="empty-state-action"><a class="linkbtn btn-ico" href="/pos/new">` + iconPlus + `New Pos</a></p>
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
    <a class="pill primary btn-ico" href="/transactions/new?type=money_in">` + iconPlus + `Income</a>
    <a class="pill btn-ico" href="/transactions/new?type=money_out">` + iconMinus + `Spending</a>
    <a class="pill btn-ico" href="/pos/new">` + iconPlus + `New Pos</a>
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
          <td>{{if .ID}}<a href="/pos/{{.ID}}">{{.Name}}</a>{{else}}{{.Name}}{{end}}{{if .AccountName}} <span class="quiet">· {{.AccountName}}</span>{{end}}</td>
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

// PosOption is one Pos shown in a <select> or <datalist>. Used by the
// new-template form (line picker), the apply form, and the new-transaction
// combobox. AccountName disambiguates duplicate Pos names across accounts
// (UNIQUE is on (name, currency, account_id), so the same name may appear
// under several accounts).
type PosOption struct {
	ID          string
	Name        string
	Currency    string
	AccountName string
}

// AccountOption is one account shown in a <select> on the apply form.
type AccountOption struct {
	ID   string
	Name string
}

// IncomeTemplateOption is one template in the monthly-income shortcut
// on the new-transaction page (money_in only). The link lands on the
// template detail page where preview + one-click apply live.
type IncomeTemplateOption struct {
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
	PosID   string
	Amount  string
	Percent string // raw percent e.g. "60" — empty means fixed-only
}

// IncomeTemplateDetailData drives /income-templates/:id.
type IncomeTemplateDetailData struct {
	Title             string
	DisplayName       string
	UnreadCount       int
	ID                string
	Name              string
	Lines             []IncomeTemplateLineRow
	LinesTotal        int64
	TotalPercentBps   int32  // Σ percent lines, 0 when fixed-only
	TotalPercentLabel string // e.g. "60%" — empty when TotalPercentBps == 0
	LeftoverPosID     string
	LeftoverPosName   string
	HasLeftoverPos    bool
	Pos               []PosOption
	Accounts          []AccountOption
	Flash             string // surfaced after apply
}

func (d IncomeTemplateDetailData) SignedIn() bool { return d.DisplayName != "" }
func (d IncomeTemplateDetailData) Compact() bool  { return false }
func (d IncomeTemplateDetailData) Wide() bool     { return false }
func (d IncomeTemplateDetailData) HideBell() bool { return false }
func (d IncomeTemplateDetailData) Route() string  { return "income" }

// IncomeTemplateLineRow is one allocation row on the detail page.
type IncomeTemplateLineRow struct {
	PosID        string
	PosName      string
	PosCurrency  string
	Amount       int64
	PercentBps   int32  // 0 when fixed-only
	PercentLabel string // e.g. "60%" — empty when PercentBps == 0
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
    <a class="pill primary btn-ico" href="/income-templates/new">` + iconPlus + `New template</a>
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
<ul>
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
<label for="leftover_pos_id">Leftover Pos <span class="quiet">(optional)</span></label>
<select id="leftover_pos_id" name="leftover_pos_id">
<option value="">— none (strict: amount must equal Σ(lines)) —</option>
{{range .Pos}}
<option value="{{.ID}}"{{if eq .ID $.LeftoverPosID}} selected{{end}}>{{.Name}} ({{.Currency}}{{if .AccountName}}, {{.AccountName}}{{end}})</option>
{{end}}
</select>
<p class="hint">If set, any amount above the lines&rsquo; total lands here. Otherwise a too-large amount is rejected.</p>
</div>
<h2 class="section-h2">Lines</h2>
<div class="alloc" id="alloc-lines">
{{range $i, $line := .Lines}}
<div class="alloc-row">
<select name="pos_id_{{$i}}" aria-label="Pos for line {{$i}}">
<option value="">— skip —</option>
{{range $.Pos}}<option value="{{.ID}}"{{if eq .ID $line.PosID}} selected{{end}}>{{.Name}} ({{.Currency}}{{if .AccountName}}, {{.AccountName}}{{end}})</option>{{end}}
</select>
<input name="amount_{{$i}}" type="text" inputmode="numeric" pattern="[0-9.,]*" value="{{$line.Amount}}"
  placeholder="e.g. 12.000.000" aria-label="Amount for line {{$i}}">
<input name="percent_{{$i}}" type="text" inputmode="decimal" value="{{$line.Percent}}"
  placeholder="e.g. 60%" aria-label="Percent for line {{$i}}">
</div>
{{end}}
{{if not .Lines}}
{{range $i := (intRange 0 8)}}
<div class="alloc-row">
<select name="pos_id_{{$i}}" aria-label="Pos for line {{$i}}">
<option value="">— skip —</option>
{{range $.Pos}}<option value="{{.ID}}">{{.Name}} ({{.Currency}}{{if .AccountName}}, {{.AccountName}}{{end}})</option>{{end}}
</select>
<input name="amount_{{$i}}" type="text" inputmode="numeric" pattern="[0-9.,]*" placeholder="e.g. 12.000.000"
  aria-label="Amount for line {{$i}}">
<input name="percent_{{$i}}" type="text" inputmode="decimal" placeholder="e.g. 60%"
  aria-label="Percent for line {{$i}}">
</div>
{{end}}
{{end}}
</div>
<p class="add-line"><a href="#" class="linkbtn btn-ico" onclick="return addLine()">` + iconPlus + `Add line</a></p>
<p class="hint">Each line takes a fixed amount, a percent of the salary (e.g. 60%), or both.</p>
<button type="submit" class="btn-ico mt-4">` + iconPlus + `Create template</button>
</form>
<script>
function addLine(){var c=document.getElementById("alloc-lines"),n=c.children.length,
lc=document.getElementById("line-count"),r=c.lastElementChild.cloneNode(true),
s=r.querySelector("select"),inps=r.querySelectorAll("input");
s.name="pos_id_"+n;s.selectedIndex=0;
for(var k=0;k<inps.length;k++){inps[k].name=(inps[k].name.indexOf("percent_")===0?"percent_":"amount_")+n;inps[k].value="";}
c.appendChild(r);lc.value=n+1;return false}
</script>
<p class="aside"><a class="linkbtn" href="/income-templates">&larr; Back</a></p>`

const incomeTemplateDetailBody = `<header class="page-head">
  <div>
    <p class="home-eyebrow">Income template</p>
    <h1>{{.Name}}</h1>
  </div>
</header>
<p class="subtitle">Allocation total {{money .LinesTotal "idr"}}{{if .TotalPercentLabel}} + {{.TotalPercentLabel}} of salary{{end}}{{if .HasLeftoverPos}} &middot; leftover → <strong>{{.LeftoverPosName}}</strong>{{end}}</p>
{{if .Flash}}<p class="flash" role="status">{{.Flash}}</p>{{end}}

<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Edit template</span>
  </div>
  <div class="card-pad">
  <form method="post" action="/income-templates/{{.ID}}/edit" id="edit-form">
  <input type="hidden" name="line_count" id="edit-line-count" value="{{if .Lines}}{{len .Lines}}{{else}}1{{end}}">
  <div class="field">
  <label for="edit-name">Name</label>
  <input id="edit-name" name="name" type="text" value="{{.Name}}" required maxlength="80">
  </div>
  <div class="field">
  <label for="edit-leftover">Leftover Pos <span class="quiet">(optional)</span></label>
  <select id="edit-leftover" name="leftover_pos_id">
  <option value="">— none —</option>
  {{range .Pos}}<option value="{{.ID}}"{{if eq .ID $.LeftoverPosID}} selected{{end}}>{{.Name}} ({{.Currency}}{{if .AccountName}}, {{.AccountName}}{{end}})</option>{{end}}
  </select>
  </div>
  <h3 class="section-h3">Lines</h3>
  <div class="alloc" id="edit-alloc-lines">
  {{if .Lines}}
  {{range $i, $line := .Lines}}
  <div class="alloc-row">
  <select name="pos_id_{{$i}}">
  <option value="">— remove —</option>
  {{range $.Pos}}<option value="{{.ID}}"{{if eq .ID $line.PosID}} selected{{end}}>{{.Name}} ({{.Currency}}{{if .AccountName}}, {{.AccountName}}{{end}})</option>{{end}}
  </select>
  <input name="amount_{{$i}}" type="text" inputmode="numeric" pattern="[0-9.,]*" value="{{fmtAmt $line.Amount}}"
    placeholder="e.g. 12.000.000">
  <input name="percent_{{$i}}" type="text" inputmode="decimal" value="{{$line.PercentLabel}}"
    placeholder="e.g. 60%">
  </div>
  {{end}}
  {{else}}
  <div class="alloc-row">
  <select name="pos_id_0">
  <option value="">— skip —</option>
  {{range $.Pos}}<option value="{{.ID}}">{{.Name}} ({{.Currency}}{{if .AccountName}}, {{.AccountName}}{{end}})</option>{{end}}
  </select>
  <input name="amount_0" type="text" inputmode="numeric" pattern="[0-9.,]*" placeholder="e.g. 12.000.000">
  <input name="percent_0" type="text" inputmode="decimal" placeholder="e.g. 60%">
  </div>
  {{end}}
  </div>
  <p class="add-line"><a href="#" class="linkbtn btn-ico" onclick="return addEditLine()">` + iconPlus + `Add line</a></p>
  <button type="submit" class="btn-ico mt-4">` + iconCheck + `Save changes</button>
  </form>
  </div>
</section>

<section class="section-card">
  <div class="section-card-head">
    <span class="section-card-title">Apply &rarr; preview allocation</span>
  </div>
  <div class="card-pad">
  <p class="subtitle">Enter the details; the next step shows the suggested split — you can adjust it before approving.</p>
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
  <button type="submit" class="btn-ico">` + iconEye + `Preview allocation</button>
  </form>
  </div>
</section>
<script>
function addEditLine(){var c=document.getElementById("edit-alloc-lines"),n=c.children.length,
lc=document.getElementById("edit-line-count"),r=c.lastElementChild.cloneNode(true),
s=r.querySelector("select"),inps=r.querySelectorAll("input");
s.name="pos_id_"+n;s.selectedIndex=0;
for(var k=0;k<inps.length;k++){inps[k].name=(inps[k].name.indexOf("percent_")===0?"percent_":"amount_")+n;inps[k].value="";}
c.appendChild(r);lc.value=n+1;return false}
</script>
<p class="aside"><a class="linkbtn" href="/income-templates">&larr; All templates</a></p>`

const incomeTemplatePreviewBody = `<h1>Review allocation</h1>
<p class="subtitle">Template &middot; <strong>{{.TemplateName}}</strong></p>

<section class="section-card">
  <div class="section-card-head"><span class="section-card-title">Incoming</span></div>
  <div class="table-wrap">
<table>
<tbody>
<tr><td>Amount</td><td class="num"><strong>{{money .Amount "idr"}}</strong></td></tr>
<tr><td>Date</td><td class="num">{{.EffectiveDate}}</td></tr>
<tr><td>Counterparty</td><td class="num">{{.CounterpartyName}}</td></tr>
</tbody>
</table>
  </div>
</section>

{{if .SuggestionNotice}}
<div class="alert" role="alert">{{.SuggestionNotice}}</div>
{{end}}

<form method="post" action="/income-templates/{{.ID}}/apply">
<input type="hidden" name="amount" value="{{.AmountRaw}}">
<input type="hidden" name="effective_date" value="{{.EffectiveDate}}">
<input type="hidden" name="counterparty_name" value="{{.CounterpartyName}}">
<input type="hidden" name="idempotency_key" value="{{.IdempotencyKey}}">

<section class="section-card">
  <div class="section-card-head"><span class="section-card-title">Allocation</span></div>
  <div class="card-pad">
<p class="subtitle">Adjust as needed. The rows must sum to {{money .Amount "idr"}}.</p>
<div class="alloc">
{{range $i, $row := .Rows}}
<div class="alloc-row">
<select name="alloc_pos_{{$i}}" aria-label="Pos for row {{$i}}">
<option value="">— skip —</option>
{{range $.PosOptions}}<option value="{{.ID}}"{{if eq .ID $row.PosID}} selected{{end}}>{{.Name}} ({{.Currency}}{{if .AccountName}}, {{.AccountName}}{{end}})</option>{{end}}
</select>
<input name="alloc_amount_{{$i}}" type="text" inputmode="numeric" pattern="[0-9.,]*"
  value="{{$row.Amount}}" placeholder="e.g. 12.000.000" aria-label="Amount for row {{$i}}">
</div>
{{end}}
<div class="alloc-total"><span>Salary total to allocate</span><strong>{{money .Amount "idr"}}</strong></div>
</div>
<p class="hint">Tip: leave a row empty to drop it. Add a Pos to a previously-empty row to introduce a new line. Each Pos can appear only once.</p>
  </div>
</section>

<button type="submit" class="btn-ico">` + iconCheck + `Approve and apply</button>
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
    <a class="pill primary btn-ico" href="/accounts/new">` + iconPlus + `New Account</a>
  </div>
</header>

{{if .Flash}}<p class="flash" role="status">{{.Flash}}</p>{{end}}
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
    <form method="post" action="/accounts/{{.ID}}/rename" class="inline-row-form">
      <input type="text" name="name" value="{{.Name}}" required maxlength="80" aria-label="Rename {{.Name}}">
      <button type="submit" class="btn-sm btn-outline btn-ico">` + iconPencil + `Rename</button>
    </form>
  </td>
  <td>{{if .Archived}}<span class="chip">archived</span>{{else}}<span class="chip chip-in">active</span>{{end}}</td>
  <td><div class="inline-row-actions">
  {{if not .Archived}}
    <form method="post" action="/accounts/{{.ID}}/archive" onsubmit="return confirm('Archive this account? Existing Pos still pointing at it will keep working but the account will be hidden from default lists.');">
      <button type="submit" class="btn-sm btn-outline btn-ico">` + iconArchive + `Archive</button>
    </form>
  {{end}}
    <form method="post" action="/accounts/{{.ID}}/delete" onsubmit="return confirm('Delete this account permanently? Only succeeds if no Pos points at it.');">
      <button type="submit" class="btn-sm btn-danger-outline btn-ico">` + iconTrash + `Delete</button>
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
<ul>
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
<button type="submit" class="btn-ico">` + iconPlus + `Create Account</button>
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
  <div class="card-pad">
    <p class="subtitle">Choose how the app looks on this device. <strong>Auto</strong> follows your system preference.</p>
    <form method="post" action="/settings/theme" class="theme-switch">
    <button type="submit" name="theme" value="light" class="btn-ico{{if eq .CurrentTheme "light"}} active{{end}}">` + iconSun + `Light</button>
    <button type="submit" name="theme" value="dark" class="btn-ico{{if eq .CurrentTheme "dark"}} active{{end}}">` + iconMoon + `Dark</button>
    <button type="submit" name="theme" value="auto" class="btn-ico{{if eq .CurrentTheme "auto"}} active{{end}}">` + iconMonitor + `Auto (follow OS)</button>
    </form>
  </div>
</section>`
