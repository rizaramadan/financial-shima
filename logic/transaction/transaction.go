// Package transaction encodes spec §5.1 validation rules for the three
// transaction types: money_in, money_out, inter_pos. The package is pure —
// it does not load anything from a DB; the caller hydrates AccountRef,
// PosRef, and `today` and we return the list of rule violations.
//
// We return ALL violations (not the first one) so a UI can show every
// problem in a single render pass.
package transaction

import (
	"regexp"
	"strings"
	"time"

	"github.com/rizaramadan/financial-shima/logic/money"
)

// Type enumerates the three transaction shapes.
type Type string

const (
	MoneyIn  Type = "money_in"
	MoneyOut Type = "money_out"
	InterPos Type = "inter_pos"
)

// Mode applies only to InterPos. spec §4.3.
type Mode string

const (
	ModeReallocation Mode = "reallocation"
	ModeBorrow       Mode = "borrow"
)

// Direction labels each line of an inter_pos transaction. spec §4.3.
type Direction string

const (
	DirOut Direction = "out"
	DirIn  Direction = "in"
)

// AccountRef is the validation-relevant slice of an Account record.
type AccountRef struct {
	ID       string
	Archived bool
}

// PosRef is the validation-relevant slice of a Pos record. Currency is
// always lowercased per logic/money's convention.
type PosRef struct {
	ID       string
	Currency string
	Archived bool
}

// MoneyInput drives ValidateMoneyIn and ValidateMoneyOut.
type MoneyInput struct {
	EffectiveDate    time.Time
	Account          AccountRef
	AccountAmount    money.Money // IDR per spec §4.1
	Pos              PosRef
	PosAmount        money.Money
	CounterpartyName string
}

// InterPosLine is one line of an inter_pos transaction.
type InterPosLine struct {
	Pos       PosRef
	Direction Direction
	Amount    money.Money
}

// InterPosInput drives ValidateInterPos.
type InterPosInput struct {
	EffectiveDate time.Time
	Mode          Mode
	Lines         []InterPosLine
}

// counterpartyRegex enforces spec §4.4.
var counterpartyRegex = regexp.MustCompile(`^[a-zA-Z0-9_\- ]+$`)

// ValidateMoneyIn returns all spec-§5.1 rule violations for a money_in
// transaction. An empty result means the input is valid.
func ValidateMoneyIn(in MoneyInput, today time.Time) []string {
	return validateMoneyDirected(in, today, MoneyIn)
}

// ValidateMoneyOut is symmetric to ValidateMoneyIn — same rules apply.
func ValidateMoneyOut(in MoneyInput, today time.Time) []string {
	return validateMoneyDirected(in, today, MoneyOut)
}

func validateMoneyDirected(in MoneyInput, today time.Time, _ Type) []string {
	var errs []string

	errs = append(errs, validateEffectiveDate(in.EffectiveDate, today)...)

	if in.Account.Archived {
		errs = append(errs, "account is archived")
	}
	if in.Pos.Archived {
		errs = append(errs, "pos is archived")
	}
	if in.PosAmount.Currency != in.Pos.Currency {
		errs = append(errs, "pos currency mismatch: amount in "+
			in.PosAmount.Currency+", pos is "+in.Pos.Currency)
	}
	if in.AccountAmount.Currency != "idr" {
		// spec §4.1: accounts are IDR-only.
		errs = append(errs, "account amount must be IDR")
	}
	if in.AccountAmount.Cents <= 0 {
		errs = append(errs, "account amount must be positive (sign comes from transaction type)")
	}
	if in.PosAmount.Cents <= 0 {
		errs = append(errs, "pos amount must be positive (sign comes from transaction type)")
	}
	// spec §5.1: for IDR Pos: account_amount == pos_amount.
	if in.Pos.Currency == "idr" && in.AccountAmount.Cents != in.PosAmount.Cents {
		errs = append(errs, "for IDR pos, account amount must equal pos amount")
	}

	errs = append(errs, validateCounterparty(in.CounterpartyName)...)
	return errs
}

// ValidateInterPos returns all rule violations for an inter_pos transaction.
func ValidateInterPos(in InterPosInput, today time.Time) []string {
	var errs []string

	errs = append(errs, validateEffectiveDate(in.EffectiveDate, today)...)

	if in.Mode != ModeReallocation && in.Mode != ModeBorrow {
		errs = append(errs, "mode must be reallocation or borrow")
	}

	var hasOut, hasIn bool
	// per-currency totals — sum via money.Add so overflow surfaces as an
	// explicit violation rather than silently wrapping past int64 (Skeet R1).
	outByCcy := map[string]money.Money{}
	inByCcy := map[string]money.Money{}
	addInto := func(m map[string]money.Money, line money.Money, lineNo int) {
		key := line.Currency
		if _, ok := m[key]; !ok {
			m[key] = money.New(0, key)
		}
		sum, err := m[key].Add(line)
		if err != nil {
			errs = append(errs, "line "+itoa(lineNo)+": amount sum overflow")
			return
		}
		m[key] = sum
	}
	for i, l := range in.Lines {
		if l.Pos.Archived {
			errs = append(errs, "line "+itoa(i+1)+": pos is archived")
		}
		if l.Amount.Currency != l.Pos.Currency {
			errs = append(errs, "line "+itoa(i+1)+": amount currency "+
				l.Amount.Currency+" does not match pos currency "+l.Pos.Currency)
		}
		if l.Amount.Cents <= 0 {
			errs = append(errs, "line "+itoa(i+1)+": amount must be positive")
		}
		switch l.Direction {
		case DirOut:
			hasOut = true
			addInto(outByCcy, l.Amount, i+1)
		case DirIn:
			hasIn = true
			addInto(inByCcy, l.Amount, i+1)
		default:
			errs = append(errs, "line "+itoa(i+1)+": direction must be 'out' or 'in'")
		}
	}
	if !hasOut {
		errs = append(errs, "must have at least one 'out' line")
	}
	if !hasIn {
		errs = append(errs, "must have at least one 'in' line")
	}

	// spec §10.6: per-currency totals must match.
	// Cross-currency lines are not summed; each currency reconciles
	// independently.
	allCcy := map[string]struct{}{}
	for c := range outByCcy {
		allCcy[c] = struct{}{}
	}
	for c := range inByCcy {
		allCcy[c] = struct{}{}
	}
	for c := range allCcy {
		if outByCcy[c].Cents != inByCcy[c].Cents {
			errs = append(errs, "currency "+c+
				": Σ(out) "+itoa(int(outByCcy[c].Cents))+
				" != Σ(in) "+itoa(int(inByCcy[c].Cents)))
		}
	}

	return errs
}

func validateEffectiveDate(d, today time.Time) []string {
	if d.IsZero() {
		return []string{"effective_date is required"}
	}
	// Compare at day granularity in the same TZ as `today`. Future dates
	// rejected per spec §5.1.
	dDay := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, today.Location())
	tDay := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	if dDay.After(tDay) {
		return []string{"effective_date is in the future"}
	}
	return nil
}

func validateCounterparty(name string) []string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return []string{"counterparty is required"}
	}
	if strings.ContainsAny(trimmed, "\t\n") {
		return []string{"counterparty contains tab or newline"}
	}
	if !counterpartyRegex.MatchString(trimmed) {
		return []string{"counterparty contains invalid characters"}
	}
	return nil
}

func itoa(n int) string {
	// stdlib strconv would do, but a tiny inlined version keeps this file
	// dependency-free at the test-quote level for skim-readers.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
