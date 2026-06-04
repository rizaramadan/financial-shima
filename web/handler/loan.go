package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/dependencies/ledger"
	"github.com/rizaramadan/financial-shima/logic/notification"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

// LoanSessionCookie is the borrower's session cookie. It is intentionally
// distinct from the family SessionCookieName so the two auth surfaces never
// cross: a borrower token never resolves to a family user and vice versa.
const LoanSessionCookie = "loan_session"

const loanSessionTTL = 30 * 24 * time.Hour

// newLoanToken returns a 256-bit random hex token for a borrower session.
func newLoanToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// parseRupiah strips grouping (dots, commas, spaces) and parses a positive
// whole-rupiah amount. Loans are IDR-only in v1, so there are no cents.
func parseRupiah(s string) (int64, bool) {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(b.String(), 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// borrowerPosID resolves the borrower session and confirms it is bound to the
// loan id in the route. Returns false (caller redirects to login) when the
// cookie is missing/expired, the route id is malformed, or the session's
// pos_id doesn't match the route — so a borrower logged into loan A can never
// reach loan B by editing the URL.
func (h *Handlers) borrowerPosID(c echo.Context) (uuid.UUID, bool) {
	rid, err := uuid.Parse(c.Param("id"))
	if err != nil || h.DB == nil {
		return uuid.UUID{}, false
	}
	cookie, err := c.Cookie(LoanSessionCookie)
	if err != nil || cookie.Value == "" {
		return uuid.UUID{}, false
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()
	sess, err := dbq.New(h.DB).GetLoanSession(ctx, cookie.Value)
	if err != nil {
		return uuid.UUID{}, false
	}
	if uuid.UUID(sess.PosID.Bytes) != rid {
		return uuid.UUID{}, false
	}
	return rid, true
}

func loanLoginRedirect(c echo.Context) error {
	return c.Redirect(http.StatusSeeOther, "/loan/"+c.Param("id")+"/login")
}

// LoanLoginGet renders the borrower sign-in form for one loan.
func (h *Handlers) LoanLoginGet(c echo.Context) error {
	if _, err := uuid.Parse(c.Param("id")); err != nil {
		return c.Redirect(http.StatusSeeOther, "/")
	}
	// Already signed in for this loan → straight to the view.
	if _, ok := h.borrowerPosID(c); ok {
		return c.Redirect(http.StatusSeeOther, "/loan/"+c.Param("id"))
	}
	return c.Render(http.StatusOK, "loan_login", template.LoanLoginData{
		Title: "Loan access",
		PosID: c.Param("id"),
	})
}

// LoanLoginPost validates the per-loan username/password against loan_access
// and, on success, mints a DB-backed borrower session scoped to this pos.
func (h *Handlers) LoanLoginPost(c echo.Context) error {
	posID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/")
	}
	data := template.LoanLoginData{Title: "Loan access", PosID: c.Param("id")}
	if h.DB == nil {
		data.Error = "Loan access is not available right now."
		return c.Render(http.StatusOK, "loan_login", data)
	}
	username := strings.TrimSpace(c.FormValue("username"))
	password := c.FormValue("password")

	ctx, cancel := context.WithTimeout(c.Request().Context(), 4*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	access, err := q.GetLoanAccessByPos(ctx, pgtype.UUID{Bytes: posID, Valid: true})
	// Constant-ish failure path: same generic error whether the loan has no
	// access row or the password is wrong, so we don't leak which loans exist.
	if err != nil || strings.ToLower(access.Username) != strings.ToLower(username) ||
		bcrypt.CompareHashAndPassword([]byte(access.PasswordHash), []byte(password)) != nil {
		data.Error = "Wrong username or password."
		return c.Render(http.StatusOK, "loan_login", data)
	}

	token, err := newLoanToken()
	if err != nil {
		c.Logger().Errorf("loan login: token: %v", err)
		data.Error = "Couldn’t start your session. Try again."
		return c.Render(http.StatusOK, "loan_login", data)
	}
	if _, err := q.CreateLoanSession(ctx, dbq.CreateLoanSessionParams{
		Token:     token,
		PosID:     pgtype.UUID{Bytes: posID, Valid: true},
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(loanSessionTTL), Valid: true},
	}); err != nil {
		c.Logger().Errorf("loan login: CreateLoanSession: %v", err)
		data.Error = "Couldn’t start your session. Try again."
		return c.Render(http.StatusOK, "loan_login", data)
	}
	c.SetCookie(&http.Cookie{
		Name:     LoanSessionCookie,
		Value:    token,
		Path:     "/loan/",
		Expires:  time.Now().Add(loanSessionTTL),
		HttpOnly: true,
		Secure:   c.Request().TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
	return c.Redirect(http.StatusSeeOther, "/loan/"+c.Param("id"))
}

// LoanLogoutPost clears the borrower session.
func (h *Handlers) LoanLogoutPost(c echo.Context) error {
	if cookie, err := c.Cookie(LoanSessionCookie); err == nil && cookie.Value != "" && h.DB != nil {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
		defer cancel()
		_ = dbq.New(h.DB).DeleteLoanSession(ctx, cookie.Value)
	}
	c.SetCookie(&http.Cookie{Name: LoanSessionCookie, Value: "", Path: "/loan/", MaxAge: -1})
	return c.Redirect(http.StatusSeeOther, "/loan/"+c.Param("id")+"/login")
}

// LoanViewGet renders the borrower's single-loan view: outstanding balance,
// repayment progress, the submit form, and their submission history.
func (h *Handlers) LoanViewGet(c echo.Context) error {
	posID, ok := h.borrowerPosID(c)
	if !ok {
		return loanLoginRedirect(c)
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 4*time.Second)
	defer cancel()
	q := dbq.New(h.DB)

	pid := pgtype.UUID{Bytes: posID, Valid: true}
	pos, err := q.GetPos(ctx, pid)
	if err != nil {
		c.Logger().Errorf("loan view: GetPos: %v", err)
		return loanLoginRedirect(c)
	}
	data := loanViewData(c, posID, pos)
	balance, err := q.GetPosCashBalance(ctx, pid)
	if err != nil {
		c.Logger().Errorf("loan view: GetPosCashBalance: %v", err)
	}
	fillLoanProgress(&data, balance)
	if subs, err := q.ListLoanSubmissionsByPos(ctx, pid); err != nil {
		c.Logger().Errorf("loan view: ListLoanSubmissionsByPos: %v", err)
	} else {
		for _, s := range subs {
			data.Submissions = append(data.Submissions, loanSubmissionRow(s))
		}
	}
	if flash, _ := c.Cookie("loan_flash"); flash != nil && flash.Value != "" {
		data.Flash = flash.Value
		c.SetCookie(&http.Cookie{Name: "loan_flash", Value: "", Path: "/loan/", MaxAge: -1})
	}
	return c.Render(http.StatusOK, "loan_view", data)
}

// LoanPaymentPost records a borrower repayment submission (status=pending).
func (h *Handlers) LoanPaymentPost(c echo.Context) error {
	posID, ok := h.borrowerPosID(c)
	if !ok {
		return loanLoginRedirect(c)
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 4*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	pid := pgtype.UUID{Bytes: posID, Valid: true}

	payer := strings.TrimSpace(c.FormValue("payer_name"))
	amount, amountOK := parseRupiah(c.FormValue("amount"))
	dateStr := strings.TrimSpace(c.FormValue("date"))
	note := strings.TrimSpace(c.FormValue("note"))
	effDate, dateErr := time.Parse("2006-01-02", dateStr)

	if payer == "" || !amountOK || dateErr != nil {
		// Re-render with an error rather than redirect, preserving context.
		pos, err := q.GetPos(ctx, pid)
		if err != nil {
			return loanLoginRedirect(c)
		}
		data := loanViewData(c, posID, pos)
		if balance, err := q.GetPosCashBalance(ctx, pid); err == nil {
			fillLoanProgress(&data, balance)
		}
		if subs, err := q.ListLoanSubmissionsByPos(ctx, pid); err == nil {
			for _, s := range subs {
				data.Submissions = append(data.Submissions, loanSubmissionRow(s))
			}
		}
		data.Error = "Enter who paid, a valid amount, and a date."
		data.BorrowerName = payer
		return c.Render(http.StatusOK, "loan_view", data)
	}

	if _, err := q.CreateLoanSubmission(ctx, dbq.CreateLoanSubmissionParams{
		PosID:         pid,
		PayerName:     payer,
		Amount:        amount,
		EffectiveDate: pgtype.Date{Time: effDate, Valid: true},
		Note:          ptrOrNil(note),
	}); err != nil {
		c.Logger().Errorf("loan payment: CreateLoanSubmission: %v", err)
		c.SetCookie(&http.Cookie{Name: "loan_flash", Value: "Couldn’t submit — try again.", Path: "/loan/"})
		return c.Redirect(http.StatusSeeOther, "/loan/"+c.Param("id"))
	}
	c.SetCookie(&http.Cookie{Name: "loan_flash", Value: "Payment submitted — awaiting approval.", Path: "/loan/"})
	return c.Redirect(http.StatusSeeOther, "/loan/"+c.Param("id"))
}

// LoanCancelPost lets a borrower cancel their own pending submission.
func (h *Handlers) LoanCancelPost(c echo.Context) error {
	posID, ok := h.borrowerPosID(c)
	if !ok {
		return loanLoginRedirect(c)
	}
	subID, err := uuid.Parse(c.Param("sid"))
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/loan/"+c.Param("id"))
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()
	// pos_id is part of the WHERE so a borrower can only cancel their own loan's rows.
	if _, err := dbq.New(h.DB).CancelLoanSubmission(ctx, dbq.CancelLoanSubmissionParams{
		ID:    pgtype.UUID{Bytes: subID, Valid: true},
		PosID: pgtype.UUID{Bytes: posID, Valid: true},
	}); err != nil {
		c.Logger().Errorf("loan cancel: %v", err)
	}
	c.SetCookie(&http.Cookie{Name: "loan_flash", Value: "Submission cancelled.", Path: "/loan/"})
	return c.Redirect(http.StatusSeeOther, "/loan/"+c.Param("id"))
}

// ── view-model helpers ────────────────────────────────────────────────

func loanViewData(c echo.Context, posID uuid.UUID, pos dbq.Po) template.LoanViewData {
	d := template.LoanViewData{
		Title:    pos.Name,
		PosID:    posID.String(),
		LoanName: pos.Name,
		Currency: pos.Currency,
		TodayISO: c.QueryParam("today"), // tests can pin a date; empty = browser default
	}
	if pos.Target != nil {
		d.Target = *pos.Target
	}
	return d
}

// fillLoanProgress sets Balance/Outstanding/PctRepaid from the pos cash
// balance (= repaid-so-far, since fund + disburse net to zero at setup).
func fillLoanProgress(d *template.LoanViewData, balance int64) {
	if balance < 0 {
		balance = 0
	}
	d.Balance = balance
	d.Outstanding = d.Target - balance
	if d.Outstanding < 0 {
		d.Outstanding = 0
	}
	if d.Target > 0 {
		pct := int(balance * 100 / d.Target)
		if pct > 100 {
			pct = 100
		}
		d.PctRepaid = pct
	}
}

func loanSubmissionRow(s dbq.LoanPaymentSubmission) template.LoanSubmissionRow {
	row := template.LoanSubmissionRow{
		ID:        uuid.UUID(s.ID.Bytes).String(),
		PayerName: s.PayerName,
		Amount:    s.Amount,
		Status:    string(s.Status),
		CanCancel: string(s.Status) == "pending",
	}
	if s.EffectiveDate.Valid {
		row.Date = s.EffectiveDate.Time.Format("2 Jan 2006")
	}
	if s.Note != nil {
		row.Note = *s.Note
	}
	if s.RejectReason != nil {
		row.Reason = *s.RejectReason
	}
	return row
}

func ptrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ── family-only: set up a loan ────────────────────────────────────────

// LoanSetupGet renders the "Set up loan" form (family auth required).
func (h *Handlers) LoanSetupGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	data := template.LoanSetupData{Title: "Set up a loan", DisplayName: u.DisplayName}
	if h.DB == nil {
		data.Errors = []string{"Database is not configured."}
		return c.Render(http.StatusOK, "loan_setup", data)
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()
	data.UnreadCount = h.loadBellCount(ctx, c, u.ID)
	if accs, err := dbq.New(h.DB).ListAccounts(ctx); err == nil {
		for _, a := range accs {
			data.Accounts = append(data.Accounts, template.AccountOption{
				ID: uuid.UUID(a.ID.Bytes).String(), Name: a.Name,
			})
		}
	}
	return c.Render(http.StatusOK, "loan_setup", data)
}

// LoanSetupPost creates the loan: a Pos (target = amount, is_loan), a funding
// money_in and a disbursing money_out (net balance 0), and the borrower login.
// The two money entries go through dependencies/ledger so they obey the same
// append-only/idempotency invariants as every other transaction.
func (h *Handlers) LoanSetupPost(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	form := template.LoanSetupData{
		Title:        "Set up a loan",
		DisplayName:  u.DisplayName,
		Name:         strings.TrimSpace(c.FormValue("name")),
		BorrowerName: strings.TrimSpace(c.FormValue("borrower_name")),
		AmountRaw:    strings.TrimSpace(c.FormValue("amount")),
		AccountID:    strings.TrimSpace(c.FormValue("account_id")),
		FundedFrom:   strings.TrimSpace(c.FormValue("funded_from")),
		Username:     strings.TrimSpace(c.FormValue("username")),
	}
	password := c.FormValue("password")

	reRender := func(msgs ...string) error {
		form.Errors = msgs
		if h.DB != nil {
			ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
			defer cancel()
			if accs, err := dbq.New(h.DB).ListAccounts(ctx); err == nil {
				for _, a := range accs {
					form.Accounts = append(form.Accounts, template.AccountOption{
						ID: uuid.UUID(a.ID.Bytes).String(), Name: a.Name,
					})
				}
			}
		}
		return c.Render(http.StatusOK, "loan_setup", form)
	}

	if h.DB == nil {
		return reRender("Database is not configured.")
	}
	amount, amountOK := parseRupiah(form.AmountRaw)
	accountID, accErr := uuid.Parse(form.AccountID)
	switch {
	case form.Name == "":
		return reRender("Loan name is required.")
	case form.BorrowerName == "":
		return reRender("Borrower name is required.")
	case !amountOK:
		return reRender("Enter a valid loan amount.")
	case accErr != nil:
		return reRender("Choose the cash account.")
	case form.FundedFrom == "":
		return reRender("Tell us where the loan is funded from.")
	case form.Username == "" || password == "":
		return reRender("Borrower username and password are required.")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 8*time.Second)
	defer cancel()
	q := dbq.New(h.DB)

	if _, err := q.GetLoanAccessByUsername(ctx, form.Username); err == nil {
		return reRender("That borrower username is already taken.")
	} else if err != pgx.ErrNoRows {
		c.Logger().Errorf("loan setup: GetLoanAccessByUsername: %v", err)
		return reRender("Couldn’t set up the loan — try again.")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		c.Logger().Errorf("loan setup: bcrypt: %v", err)
		return reRender("Couldn’t set up the loan — try again.")
	}

	pos, err := q.CreatePos(ctx, dbq.CreatePosParams{
		Name:      form.Name,
		Currency:  "idr",
		AccountID: pgtype.UUID{Bytes: accountID, Valid: true},
		Target:    &amount,
	})
	if err != nil {
		c.Logger().Errorf("loan setup: CreatePos: %v", err)
		return reRender("Couldn’t create the loan Pos — try again.")
	}
	posID := uuid.UUID(pos.ID.Bytes)
	if err := q.SetPosIsLoan(ctx, dbq.SetPosIsLoanParams{ID: pos.ID, IsLoan: true}); err != nil {
		c.Logger().Errorf("loan setup: SetPosIsLoan: %v", err)
	}

	// Fund (money_in) then disburse (money_out): net balance 0; repayments
	// climb from there. Counterparties: funding source in, borrower out.
	uid := uidPtr(u.ID)
	if err := h.loanMoneyEntry(ctx, "money_in", posID, accountID, amount, form.FundedFrom,
		"Loan funding", uid, "loan-fund-"+posID.String()); err != nil {
		c.Logger().Errorf("loan setup: fund: %v", err)
		return reRender("Loan Pos created, but funding it failed. Check /pos/" + posID.String() + ".")
	}
	if err := h.loanMoneyEntry(ctx, "money_out", posID, accountID, amount, form.BorrowerName,
		"Loan disbursement to "+form.BorrowerName, uid, "loan-disb-"+posID.String()); err != nil {
		c.Logger().Errorf("loan setup: disburse: %v", err)
		return reRender("Loan Pos funded, but disbursement failed. Check /pos/" + posID.String() + ".")
	}

	if _, err := q.CreateLoanAccess(ctx, dbq.CreateLoanAccessParams{
		PosID:        pos.ID,
		Username:     form.Username,
		PasswordHash: string(hash),
	}); err != nil {
		c.Logger().Errorf("loan setup: CreateLoanAccess: %v", err)
		return reRender("Loan created, but the borrower login failed. Set it from the Pos page.")
	}

	c.SetCookie(&http.Cookie{Name: "pos_flash", Value: "Loan created. Borrower can sign in at /loan/" + posID.String() + "/login", Path: "/"})
	return c.Redirect(http.StatusSeeOther, "/pos/"+posID.String())
}

// loanMoneyEntry appends a money_in/out through the ledger, resolving the
// counterparty by name. account_amount == pos_amount (IDR loans, v1).
func (h *Handlers) loanMoneyEntry(ctx context.Context, typ string, posID, accountID uuid.UUID,
	amount int64, counterpartyName, note string, createdBy *uuid.UUID, idemKey string) error {
	cp, err := dbq.New(h.DB).GetOrCreateCounterparty(ctx, counterpartyName)
	if err != nil {
		return err
	}
	svc := &ledger.Service{Pool: h.DB, Users: h.Auth.Users}
	_, err = svc.Insert(ctx, ledger.MoneyTxnInput{
		Type:           typ,
		EffectiveDate:  pgtype.Date{Time: time.Now(), Valid: true},
		AccountAmount:  amount,
		PosID:          posID,
		PosAmount:      amount,
		CounterpartyID: uuid.UUID(cp.ID.Bytes),
		Note:           note,
		Source:         notification.SourceWeb,
		CreatedBy:      createdBy,
		IdempotencyKey: idemKey,
	})
	return err
}

func uidPtr(id string) *uuid.UUID {
	if u, err := uuid.Parse(id); err == nil {
		return &u
	}
	return nil
}

func uidPg(id string) pgtype.UUID {
	if u, err := uuid.Parse(id); err == nil {
		return pgtype.UUID{Bytes: u, Valid: true}
	}
	return pgtype.UUID{}
}

// ── family-only: approve / reject a repayment ─────────────────────────

// LoanApprovePost approves a pending repayment: it appends a money_in via the
// ledger (idempotent on the submission id, so a double-click can't double-pay)
// and links the resulting transaction to the submission. The two steps aren't
// in one DB tx, but the idempotency key makes a retry safe — a re-approve
// returns the same txn and the guarded UPDATE is a no-op once approved.
func (h *Handlers) LoanApprovePost(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	subID, err := uuid.Parse(c.Param("sid"))
	if err != nil || h.DB == nil {
		return c.Redirect(http.StatusSeeOther, "/pos")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 6*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	sub, err := q.GetLoanSubmission(ctx, pgtype.UUID{Bytes: subID, Valid: true})
	if err != nil {
		c.Logger().Errorf("loan approve: GetLoanSubmission: %v", err)
		return c.Redirect(http.StatusSeeOther, "/pos")
	}
	posID := uuid.UUID(sub.PosID.Bytes)
	posPath := "/pos/" + posID.String()
	if string(sub.Status) != "pending" {
		c.SetCookie(&http.Cookie{Name: "pos_flash", Value: "That submission was already decided.", Path: "/"})
		return c.Redirect(http.StatusSeeOther, posPath)
	}

	cp, err := q.GetOrCreateCounterparty(ctx, sub.PayerName)
	if err != nil {
		c.Logger().Errorf("loan approve: counterparty: %v", err)
		c.SetCookie(&http.Cookie{Name: "pos_error", Value: "Couldn’t approve — try again.", Path: "/"})
		return c.Redirect(http.StatusSeeOther, posPath)
	}
	note := "Loan repayment from " + sub.PayerName
	if sub.Note != nil && *sub.Note != "" {
		note = *sub.Note
	}
	svc := &ledger.Service{Pool: h.DB, Users: h.Auth.Users}
	txnID, err := svc.Insert(ctx, ledger.MoneyTxnInput{
		Type:           "money_in",
		EffectiveDate:  sub.EffectiveDate,
		AccountAmount:  sub.Amount,
		PosID:          posID,
		PosAmount:      sub.Amount,
		CounterpartyID: uuid.UUID(cp.ID.Bytes),
		Note:           note,
		Source:         notification.SourceWeb,
		CreatedBy:      uidPtr(u.ID),
		IdempotencyKey: "loan-repay-" + subID.String(),
	})
	if err != nil {
		c.Logger().Errorf("loan approve: ledger.Insert: %v", err)
		c.SetCookie(&http.Cookie{Name: "pos_error", Value: "Couldn’t record the repayment — try again.", Path: "/"})
		return c.Redirect(http.StatusSeeOther, posPath)
	}
	if _, err := q.ApproveLoanSubmission(ctx, dbq.ApproveLoanSubmissionParams{
		ID:            sub.ID,
		DecidedBy:     uidPg(u.ID),
		TransactionID: pgtype.UUID{Bytes: txnID, Valid: true},
	}); err != nil {
		c.Logger().Errorf("loan approve: ApproveLoanSubmission: %v", err)
	}
	c.SetCookie(&http.Cookie{Name: "pos_flash", Value: "Repayment approved and recorded.", Path: "/"})
	return c.Redirect(http.StatusSeeOther, posPath)
}

// LoanRejectPost rejects a pending repayment with an optional reason. No
// ledger entry is written.
func (h *Handlers) LoanRejectPost(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	subID, err := uuid.Parse(c.Param("sid"))
	if err != nil || h.DB == nil {
		return c.Redirect(http.StatusSeeOther, "/pos")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 4*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	sub, err := q.GetLoanSubmission(ctx, pgtype.UUID{Bytes: subID, Valid: true})
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/pos")
	}
	posPath := "/pos/" + uuid.UUID(sub.PosID.Bytes).String()
	if _, err := q.RejectLoanSubmission(ctx, dbq.RejectLoanSubmissionParams{
		ID:           sub.ID,
		DecidedBy:    uidPg(u.ID),
		RejectReason: ptrOrNil(strings.TrimSpace(c.FormValue("reason"))),
	}); err != nil {
		c.Logger().Errorf("loan reject: %v", err)
	}
	c.SetCookie(&http.Cookie{Name: "pos_flash", Value: "Submission rejected.", Path: "/"})
	return c.Redirect(http.StatusSeeOther, posPath)
}
