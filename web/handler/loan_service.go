package handler

// Shared loan create / payment / decide path (same pattern as
// pos_service.go and transfer_service.go). The API handlers
// (api_loans.go) resolve their JSON transport into the neutral inputs
// below and map the sentinels to status codes; the web handlers in
// loan.go keep their form/redirect flow untouched.
//
// Money stays integer cents (whole rupiah for IDR loans, v1); the
// service never calls time.Now — the caller passes the clock value
// (fund/disburse effective date) so tests inject a fixed time.
//
// Auth model on /api/v1: the x-api-key gate IS the session (family
// trust), so there is no borrower login/logout here. Loan scoping is
// enforced by path: cancel is nested under /loans/:id and 404s when
// the submission belongs to another loan, mirroring the borrower
// cookie check in loan.go.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/dependencies/ledger"
	"github.com/rizaramadan/financial-shima/logic/notification"
	"github.com/rizaramadan/financial-shima/logic/user"
)

// LoanSetupInput is the transport-neutral shape a loan setup maps
// into. AccountID is already UUID-parsed by the caller (form vs JSON
// parsing stays in the handler). Amount is whole rupiah (int64, never
// float64). Now is the validation/effective-date clock — the caller
// passes handlerNow.
type LoanSetupInput struct {
	Name         string
	BorrowerName string
	Amount       int64
	AccountID    uuid.UUID
	FundedFrom   string
	Username     string
	Password     string
	Now          time.Time
}

// LoanPaymentInput is the transport-neutral shape a repayment
// submission maps into. EffectiveDate is already date-parsed by the
// caller; the zero value means "not provided" and fails validation.
type LoanPaymentInput struct {
	PosID         uuid.UUID
	PayerName     string
	Amount        int64
	EffectiveDate time.Time
	Note          string
}

// loanNotFoundError marks a missing loan / account / submission so
// callers map it to 404 (vs 500 for real DB failures).
type loanNotFoundError struct{ msg string }

func (e *loanNotFoundError) Error() string { return e.msg }

// IsLoanNotFound reports whether err is a missing-resource failure.
func IsLoanNotFound(err error) bool {
	var nf *loanNotFoundError
	return errors.As(err, &nf)
}

// loanConflictError marks a setup/decide rejected by a uniqueness or
// state guard (borrower username taken, submission already decided)
// so callers map it to 409 (vs 500).
type loanConflictError struct{ msg string }

func (e *loanConflictError) Error() string { return e.msg }

// IsLoanConflict reports whether err is a uniqueness/state failure.
func IsLoanConflict(err error) bool {
	var ce *loanConflictError
	return errors.As(err, &ce)
}

// APILoanSubmission is the JSON shape for one repayment submission.
// DecidedAt/RejectReason/TransactionID ride omitempty: they are null
// until the family decides.
type APILoanSubmission struct {
	ID            string            `json:"id"`
	PosID         string            `json:"pos_id"`
	PayerName     string            `json:"payer_name"`
	Amount        int64             `json:"amount"`
	EffectiveDate string            `json:"effective_date"`
	Note          string            `json:"note,omitempty"`
	Status        string            `json:"status"`
	SubmittedAt   time.Time         `json:"submitted_at"`
	DecidedAt     *time.Time        `json:"decided_at,omitempty"`
	RejectReason  *string           `json:"reject_reason,omitempty"`
	TransactionID *string           `json:"transaction_id,omitempty"`
	Links         map[string]string `json:"_links,omitempty"`
	Actions       []MoneyAction     `json:"_actions,omitempty"`
}

// APILoan is the JSON shape for one loan in /api/v1 responses.
// Balance is repaid-so-far (fund + disburse net to zero at setup);
// Outstanding/ PctRepaid derive from it via calcLoanProgress (pure).
// Submissions is `[]` (never null) on empty so callers parse one shape.
type APILoan struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Currency    string              `json:"currency"`
	AccountID   string              `json:"account_id"`
	Target      *int64              `json:"target"`
	Balance     int64               `json:"balance"`
	Outstanding int64               `json:"outstanding"`
	PctRepaid   int                 `json:"pct_repaid"`
	Submissions []APILoanSubmission `json:"submissions"`
	CreatedAt   time.Time           `json:"created_at"`
	Links       map[string]string   `json:"_links,omitempty"`
	Actions     []MoneyAction       `json:"_actions,omitempty"`
}

// calcLoanProgress derives Outstanding/PctRepaid from the pos cash
// balance (= repaid-so-far). Pure — unit-tested without a database.
// Mirrors fillLoanProgress in loan.go (web view-model); kept separate
// so neither surface can drift the other's clamps by accident — the
// table test pins both edges here.
func calcLoanProgress(target, balance int64) (outstanding int64, pct int) {
	if balance < 0 {
		balance = 0
	}
	outstanding = target - balance
	if outstanding < 0 {
		outstanding = 0
	}
	if target > 0 {
		pct = int(balance * 100 / target)
		if pct > 100 {
			pct = 100
		}
	}
	return outstanding, pct
}

// validateLoanSetupInput runs the fail-fast shape checks that need no
// DB I/O. Pure — unit-tested without a database. Mirrors the web
// LoanSetupPost switch (same messages minus form wording).
func validateLoanSetupInput(in LoanSetupInput) []string {
	switch {
	case strings.TrimSpace(in.Name) == "":
		return []string{"name is required"}
	case strings.TrimSpace(in.BorrowerName) == "":
		return []string{"borrower_name is required"}
	case in.Amount <= 0:
		return []string{"amount must be a positive whole-rupiah value"}
	case strings.TrimSpace(in.FundedFrom) == "":
		return []string{"funded_from is required"}
	case strings.TrimSpace(in.Username) == "" || in.Password == "":
		return []string{"borrower username and password are required"}
	}
	return nil
}

// validateLoanPaymentInput runs the fail-fast shape checks that need
// no DB I/O. Pure — unit-tested without a database. Mirrors the web
// LoanPaymentPost guard (same three fields).
func validateLoanPaymentInput(in LoanPaymentInput) []string {
	switch {
	case strings.TrimSpace(in.PayerName) == "":
		return []string{"payer_name is required"}
	case in.Amount <= 0:
		return []string{"amount must be a positive whole-rupiah value"}
	case in.EffectiveDate.IsZero():
		return []string{"effective_date must be YYYY-MM-DD"}
	}
	return nil
}

// loanLinks builds the HATEOAS-lite _links for a loan. Pure. Hrefs
// target existing GETs only.
func loanLinks(loanID, accountID string) map[string]string {
	return map[string]string{
		"self":         "/api/v1/loans/" + loanID,
		"pos":          "/api/v1/pos/" + loanID,
		"transactions": "/api/v1/transactions?pos_id=" + loanID,
		"balances":     "/api/v1/balances",
	}
}

// loanActions builds the _actions for a loan. The submit affordance is
// a POST (unlike the GET-only actions on pos/transfer) because taking
// a repayment IS this resource's write surface.
func loanActions(loanID string) []MoneyAction {
	return []MoneyAction{
		{Name: "self", Method: "GET", Href: "/api/v1/loans/" + loanID},
		{Name: "submit_payment", Method: "POST", Href: "/api/v1/loans/" + loanID + "/payments"},
	}
}

// loanSubmissionLinks builds the _links for one submission. Pure.
func loanSubmissionLinks(subID, posID string) map[string]string {
	return map[string]string{
		"loan":        "/api/v1/loans/" + posID,
		"transaction": "/api/v1/transactions",
		"submission":  "/api/v1/loan-submissions/" + subID,
	}
}

// loanSubmissionActions builds the _actions for one submission. Only
// pending rows advertise the state-changing affordances; decided rows
// carry none (the server guards with 409 either way).
func loanSubmissionActions(sub APILoanSubmission) []MoneyAction {
	if sub.Status != "pending" {
		return nil
	}
	return []MoneyAction{
		{Name: "approve", Method: "POST", Href: "/api/v1/loan-submissions/" + sub.ID + "/approve"},
		{Name: "reject", Method: "POST", Href: "/api/v1/loan-submissions/" + sub.ID + "/reject"},
		{Name: "cancel", Method: "POST", Href: "/api/v1/loans/" + sub.PosID + "/payments/" + sub.ID + "/cancel"},
	}
}

// loanSubmissionToAPI converts one row to the wire shape. Pure.
func loanSubmissionToAPI(s dbq.LoanPaymentSubmission) APILoanSubmission {
	id := uuid.UUID(s.ID.Bytes).String()
	posID := uuid.UUID(s.PosID.Bytes).String()
	out := APILoanSubmission{
		ID:            id,
		PosID:         posID,
		PayerName:     s.PayerName,
		Amount:        s.Amount,
		EffectiveDate: s.EffectiveDate.Time.Format("2006-01-02"),
		Status:        string(s.Status),
		SubmittedAt:   s.SubmittedAt.Time,
		Links:         loanSubmissionLinks(id, posID),
	}
	if s.Note != nil {
		out.Note = *s.Note
	}
	if s.DecidedAt.Valid {
		t := s.DecidedAt.Time
		out.DecidedAt = &t
	}
	if s.RejectReason != nil {
		out.RejectReason = s.RejectReason
	}
	if s.TransactionID.Valid {
		txn := uuid.UUID(s.TransactionID.Bytes).String()
		out.TransactionID = &txn
	}
	out.Actions = loanSubmissionActions(out)
	return out
}

// setupLoan validates, then creates the loan: a Pos (target =
// amount, is_loan), a funding money_in and a disbursing money_out
// (net balance 0), and the borrower login. The two money entries go
// through dependencies/ledger so they obey the same append-only /
// idempotency invariants as every other transaction.
//
// Returns:
//   - (result, nil, nil) on success (fully-populated detail, no submissions yet).
//   - (zero, violations, nil) on bad input — caller maps to 400.
//   - (zero, nil, *loanNotFoundError) on unknown account — caller maps to 404.
//   - (zero, nil, *loanConflictError) on taken username or duplicate
//     loan name — caller maps to 409.
//   - (zero, nil, err) on DB failure — caller maps to 500.
func setupLoan(
	ctx context.Context,
	pool *pgxpool.Pool,
	users []user.User,
	in LoanSetupInput,
) (APILoan, []string, error) {
	var zero APILoan

	if violations := validateLoanSetupInput(in); len(violations) > 0 {
		return zero, violations, nil
	}

	q := dbq.New(pool)
	if _, err := q.GetAccount(ctx, pgtype.UUID{Bytes: in.AccountID, Valid: true}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, nil, &loanNotFoundError{msg: "account not found"}
		}
		return zero, nil, err
	}
	if _, err := q.GetLoanAccessByUsername(ctx, strings.TrimSpace(in.Username)); err == nil {
		return zero, nil, &loanConflictError{msg: "borrower username already taken"}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return zero, nil, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return zero, nil, err
	}

	amount := in.Amount
	pos, err := q.CreatePos(ctx, dbq.CreatePosParams{
		Name:      strings.TrimSpace(in.Name),
		Currency:  "idr",
		AccountID: pgtype.UUID{Bytes: in.AccountID, Valid: true},
		Target:    &amount,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return zero, nil, &loanConflictError{msg: "a loan with that name already exists"}
		}
		return zero, nil, err
	}
	posID := uuid.UUID(pos.ID.Bytes)
	if err := q.SetPosIsLoan(ctx, dbq.SetPosIsLoanParams{ID: pos.ID, IsLoan: true}); err != nil {
		return zero, nil, err
	}

	// Fund (money_in) then disburse (money_out): net balance 0;
	// repayments climb from there.
	effDate := pgtype.Date{Time: in.Now, Valid: true}
	if err := insertLoanLeg(ctx, pool, users, "money_in", posID, in.AccountID,
		amount, strings.TrimSpace(in.FundedFrom), "Loan funding", effDate,
		"loan-fund-"+posID.String()); err != nil {
		return zero, nil, err
	}
	if err := insertLoanLeg(ctx, pool, users, "money_out", posID, in.AccountID,
		amount, strings.TrimSpace(in.BorrowerName), "Loan disbursement to "+strings.TrimSpace(in.BorrowerName),
		effDate, "loan-disb-"+posID.String()); err != nil {
		return zero, nil, err
	}

	if _, err := q.CreateLoanAccess(ctx, dbq.CreateLoanAccessParams{
		PosID:        pos.ID,
		Username:     strings.TrimSpace(in.Username),
		PasswordHash: string(hash),
	}); err != nil {
		return zero, nil, err
	}

	detail, err := getLoanDetail(ctx, pool, posID)
	if err != nil {
		return zero, nil, err
	}
	return detail, nil, nil
}

// insertLoanLeg appends one fund/disburse money entry through the
// ledger, resolving the counterparty by name. source=api leaves
// created_by null per spec §4.3.
func insertLoanLeg(ctx context.Context, pool *pgxpool.Pool, users []user.User,
	typ string, posID, accountID uuid.UUID, amount int64,
	counterpartyName, note string, effDate pgtype.Date, idemKey string) error {
	cp, err := dbq.New(pool).GetOrCreateCounterparty(ctx, counterpartyName)
	if err != nil {
		return err
	}
	svc := &ledger.Service{Pool: pool, Users: users}
	_, err = svc.Insert(ctx, ledger.MoneyTxnInput{
		Type:           typ,
		EffectiveDate:  effDate,
		AccountAmount:  amount,
		PosID:          posID,
		PosAmount:      amount,
		CounterpartyID: uuid.UUID(cp.ID.Bytes),
		Note:           note,
		Source:         notification.SourceAPI,
		CreatedBy:      nil,
		IdempotencyKey: idemKey,
	})
	return err
}

// submitLoanPayment records a borrower repayment submission
// (status=pending). Pending rows move no money — the balance moves on
// approve.
//
// Returns:
//   - (result, nil, nil) on success.
//   - (zero, violations, nil) on bad input — caller maps to 400.
//   - (zero, nil, *loanNotFoundError) on unknown/non-loan pos —
//     caller maps to 404.
//   - (zero, nil, err) on DB failure — caller maps to 500.
func submitLoanPayment(
	ctx context.Context,
	pool *pgxpool.Pool,
	in LoanPaymentInput,
) (APILoanSubmission, []string, error) {
	var zero APILoanSubmission

	if violations := validateLoanPaymentInput(in); len(violations) > 0 {
		return zero, violations, nil
	}

	q := dbq.New(pool)
	pos, err := q.GetPos(ctx, pgtype.UUID{Bytes: in.PosID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, nil, &loanNotFoundError{msg: "loan not found"}
		}
		return zero, nil, err
	}
	if !pos.IsLoan {
		return zero, nil, &loanNotFoundError{msg: "loan not found"}
	}

	row, err := q.CreateLoanSubmission(ctx, dbq.CreateLoanSubmissionParams{
		PosID:         pgtype.UUID{Bytes: in.PosID, Valid: true},
		PayerName:     strings.TrimSpace(in.PayerName),
		Amount:        in.Amount,
		EffectiveDate: pgtype.Date{Time: in.EffectiveDate, Valid: true},
		Note:          ptrOrNil(strings.TrimSpace(in.Note)),
	})
	if err != nil {
		return zero, nil, err
	}
	return loanSubmissionToAPI(row), nil, nil
}

// approveLoanSubmission approves a pending repayment: it appends a
// money_in via the ledger (idempotent on the submission id, so a
// retry can't double-pay) and links the resulting transaction to the
// submission. The guarded UPDATE is a no-op once approved, so a
// re-approve surfaces as 409 rather than a second ledger entry.
//
// Returns:
//   - (result, nil) on success.
//   - (zero, *loanNotFoundError) when the submission is missing —
//     caller maps to 404.
//   - (zero, *loanConflictError) when it is already decided — caller
//     maps to 409.
//   - (zero, err) on DB failure — caller maps to 500.
func approveLoanSubmission(
	ctx context.Context,
	pool *pgxpool.Pool,
	users []user.User,
	subID uuid.UUID,
) (APILoanSubmission, error) {
	var zero APILoanSubmission

	q := dbq.New(pool)
	sub, err := q.GetLoanSubmission(ctx, pgtype.UUID{Bytes: subID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, &loanNotFoundError{msg: "submission not found"}
		}
		return zero, err
	}
	if string(sub.Status) != "pending" {
		return zero, &loanConflictError{msg: "submission already decided"}
	}

	posID := uuid.UUID(sub.PosID.Bytes)
	cp, err := q.GetOrCreateCounterparty(ctx, sub.PayerName)
	if err != nil {
		return zero, err
	}
	note := "Loan repayment from " + sub.PayerName
	if sub.Note != nil && *sub.Note != "" {
		note = *sub.Note
	}
	svc := &ledger.Service{Pool: pool, Users: users}
	txnID, err := svc.Insert(ctx, ledger.MoneyTxnInput{
		Type:           "money_in",
		EffectiveDate:  sub.EffectiveDate,
		AccountAmount:  sub.Amount,
		PosID:          posID,
		PosAmount:      sub.Amount,
		CounterpartyID: uuid.UUID(cp.ID.Bytes),
		Note:           note,
		Source:         notification.SourceAPI,
		CreatedBy:      nil,
		IdempotencyKey: "loan-repay-" + subID.String(),
	})
	if err != nil {
		return zero, err
	}
	n, err := q.ApproveLoanSubmission(ctx, dbq.ApproveLoanSubmissionParams{
		ID:            sub.ID,
		DecidedBy:     pgtype.UUID{},
		TransactionID: pgtype.UUID{Bytes: txnID, Valid: true},
	})
	if err != nil {
		return zero, err
	}
	if n == 0 {
		return zero, &loanConflictError{msg: "submission already decided"}
	}
	decided, err := q.GetLoanSubmission(ctx, pgtype.UUID{Bytes: subID, Valid: true})
	if err != nil {
		return zero, err
	}
	return loanSubmissionToAPI(decided), nil
}

// rejectLoanSubmission rejects a pending repayment with an optional
// reason. No ledger entry is written. The guarded UPDATE only flips
// pending rows, so rejecting a decided submission is a 409.
//
// Returns:
//   - (result, nil) on success.
//   - (zero, *loanNotFoundError) when the submission is missing —
//     caller maps to 404.
//   - (zero, *loanConflictError) when it is already decided — caller
//     maps to 409.
//   - (zero, err) on DB failure — caller maps to 500.
func rejectLoanSubmission(
	ctx context.Context,
	pool *pgxpool.Pool,
	subID uuid.UUID,
	reason *string,
) (APILoanSubmission, error) {
	var zero APILoanSubmission

	q := dbq.New(pool)
	if _, err := q.GetLoanSubmission(ctx, pgtype.UUID{Bytes: subID, Valid: true}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, &loanNotFoundError{msg: "submission not found"}
		}
		return zero, err
	}
	n, err := q.RejectLoanSubmission(ctx, dbq.RejectLoanSubmissionParams{
		ID:           pgtype.UUID{Bytes: subID, Valid: true},
		DecidedBy:    pgtype.UUID{},
		RejectReason: reason,
	})
	if err != nil {
		return zero, err
	}
	if n == 0 {
		return zero, &loanConflictError{msg: "submission already decided"}
	}
	decided, err := q.GetLoanSubmission(ctx, pgtype.UUID{Bytes: subID, Valid: true})
	if err != nil {
		return zero, err
	}
	return loanSubmissionToAPI(decided), nil
}

// cancelLoanPayment lets the borrower side cancel their own pending
// submission. posID scopes the cancel: a submission from another loan
// 404s, mirroring the borrower-session pos check in loan.go.
//
// Returns:
//   - (result, nil) on success.
//   - (zero, *loanNotFoundError) when the submission is missing or
//     belongs to another loan — caller maps to 404.
//   - (zero, *loanConflictError) when it is already decided — caller
//     maps to 409.
//   - (zero, err) on DB failure — caller maps to 500.
func cancelLoanPayment(
	ctx context.Context,
	pool *pgxpool.Pool,
	posID, subID uuid.UUID,
) (APILoanSubmission, error) {
	var zero APILoanSubmission

	q := dbq.New(pool)
	sub, err := q.GetLoanSubmission(ctx, pgtype.UUID{Bytes: subID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, &loanNotFoundError{msg: "submission not found"}
		}
		return zero, err
	}
	if uuid.UUID(sub.PosID.Bytes) != posID {
		return zero, &loanNotFoundError{msg: "submission not found"}
	}
	if string(sub.Status) != "pending" {
		return zero, &loanConflictError{msg: "submission already decided"}
	}
	n, err := q.CancelLoanSubmission(ctx, dbq.CancelLoanSubmissionParams{
		ID:    pgtype.UUID{Bytes: subID, Valid: true},
		PosID: pgtype.UUID{Bytes: posID, Valid: true},
	})
	if err != nil {
		return zero, err
	}
	if n == 0 {
		return zero, &loanConflictError{msg: "submission already decided"}
	}
	cancelled, err := q.GetLoanSubmission(ctx, pgtype.UUID{Bytes: subID, Valid: true})
	if err != nil {
		return zero, err
	}
	return loanSubmissionToAPI(cancelled), nil
}

// getLoanDetail loads one loan with its balance-derived progress and
// submission history. Non-loan pos ids 404 (loan scoping: this
// surface only serves is_loan rows).
func getLoanDetail(
	ctx context.Context,
	pool *pgxpool.Pool,
	posID uuid.UUID,
) (APILoan, error) {
	var zero APILoan

	q := dbq.New(pool)
	pid := pgtype.UUID{Bytes: posID, Valid: true}
	pos, err := q.GetPos(ctx, pid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, &loanNotFoundError{msg: "loan not found"}
		}
		return zero, err
	}
	if !pos.IsLoan {
		return zero, &loanNotFoundError{msg: "loan not found"}
	}
	balance, err := q.GetPosCashBalance(ctx, pid)
	if err != nil {
		return zero, err
	}
	subs, err := q.ListLoanSubmissionsByPos(ctx, pid)
	if err != nil {
		return zero, err
	}

	id := uuid.UUID(pos.ID.Bytes).String()
	outstanding, pct := calcLoanProgress(derefTarget(pos.Target), balance)
	out := APILoan{
		ID:          id,
		Name:        pos.Name,
		Currency:    pos.Currency,
		AccountID:   uuid.UUID(pos.AccountID.Bytes).String(),
		Target:      pos.Target,
		Balance:     balance,
		Outstanding: outstanding,
		PctRepaid:   pct,
		Submissions: make([]APILoanSubmission, 0, len(subs)),
		CreatedAt:   pos.CreatedAt.Time,
		Links:       loanLinks(id, uuid.UUID(pos.AccountID.Bytes).String()),
		Actions:     loanActions(id),
	}
	for _, s := range subs {
		out.Submissions = append(out.Submissions, loanSubmissionToAPI(s))
	}
	return out, nil
}
