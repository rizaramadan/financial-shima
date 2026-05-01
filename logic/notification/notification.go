// Package notification encodes the spec §4.5 recipient rule as a pure
// function. It's the single source of truth that both web and API insert
// paths consult to decide whose feed gets a row.
package notification

import "github.com/rizaramadan/financial-shima/logic/user"

// Source mirrors the transaction `source` column without importing the dbq
// package (that would invert the layer dependency — Logic must not depend
// on Dependencies).
type Source string

const (
	SourceWeb  Source = "web"
	SourceAPI  Source = "api"
	SourceSeed Source = "seed"
)

// RecipientsFor returns the list of users who should receive a
// `transaction_created` notification per spec §4.5:
//
//   - source = "seed": no notifications. Initial setup never pings.
//   - source = "web":  notify all users where user_id != createdBy.
//   - source = "api":  createdBy is empty; notify all users.
//
// The function is pure — passing the same arguments always returns the
// same result. The returned slice is fresh; callers may mutate it.
func RecipientsFor(src Source, createdBy string, allUsers []user.User) []user.User {
	if src == SourceSeed {
		return nil
	}
	out := make([]user.User, 0, len(allUsers))
	for _, u := range allUsers {
		if src == SourceWeb && u.ID == createdBy {
			continue
		}
		out = append(out, u)
	}
	return out
}
