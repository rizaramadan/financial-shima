// Package pos encodes spec §4.2 validation rules for creating a Pos.
// Pure: no DB, no time. The caller passes a CreateInput; we return the
// list of rule violations (empty slice = valid).
//
// Database CHECK constraints mirror these rules; this package surfaces
// the violations to the UI before the round-trip so the user sees every
// problem in one render pass.
package pos

import (
	"regexp"
	"strings"
)

// currencyRegex enforces spec §4.2: lowercase letters, digits, hyphen.
var currencyRegex = regexp.MustCompile(`^[a-z0-9-]+$`)

// CreateInput is the validation-relevant slice of a Pos creation form.
// Target is nil when the user did not enter one (an "open" Pos with no
// budget target). HasTarget is redundant with Target!=nil but keeps the
// caller explicit.
//
// AccountID is the funding Account (spec §4.2). It's required for every
// Pos, IDR or otherwise — non-IDR Pos still trace their IDR cost
// through an Account. The handler resolves and parses the UUID before
// calling Validate; this layer only checks non-empty.
type CreateInput struct {
	Name      string
	Currency  string
	AccountID string
	Target    int64
	HasTarget bool
}

// Validate returns all spec-§4.2 rule violations for the input. An empty
// result means the input is valid.
//
// Returned strings are short user-facing sentences; the handler renders
// them verbatim into the form's error region.
func Validate(in CreateInput) []string {
	var errs []string

	if strings.TrimSpace(in.Name) == "" {
		errs = append(errs, "Name is required.")
	}

	currency := strings.TrimSpace(in.Currency)
	if currency == "" {
		errs = append(errs, "Currency is required.")
	} else if !currencyRegex.MatchString(currency) {
		errs = append(errs, "Currency must be lowercase letters, digits, or hyphens (e.g. idr, usd, gold-g).")
	}

	if strings.TrimSpace(in.AccountID) == "" {
		errs = append(errs, "Account is required.")
	}

	if in.HasTarget && in.Target < 0 {
		errs = append(errs, "Target must be zero or positive.")
	}

	return errs
}

// Normalize returns a CreateInput with Name trimmed and Currency
// trimmed + lowercased. Caller should normalize before persistence so
// dedup (UNIQUE on (name, currency)) works against canonical values.
func Normalize(in CreateInput) CreateInput {
	in.Name = strings.TrimSpace(in.Name)
	in.Currency = strings.ToLower(strings.TrimSpace(in.Currency))
	in.AccountID = strings.TrimSpace(in.AccountID)
	return in
}
