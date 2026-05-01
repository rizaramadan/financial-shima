// Package user models the two pre-seeded humans (Riza, Shima) per spec §3.1
// and answers the lookup that the login form needs.
//
// Per spec §3.1, adding/modifying users requires editing the seed and
// redeploying — there is no self-service registration. The seed lives in
// this package so the rule is enforced by `git diff` rather than runtime
// config.
package user

import "strings"

type User struct {
	ID                  string // stable identifier (e.g. "riza", "shima")
	DisplayName         string // "Riza" or "Shima" — passed verbatim to the assistant
	TelegramIdentifier  string // "@username" or numeric ID
}

// Seeded returns the canonical two-user list. The slice is fresh per call so
// callers cannot mutate package state.
func Seeded() []User {
	return []User{
		{ID: "riza", DisplayName: "Riza", TelegramIdentifier: "@riza_ramadan"},
		{ID: "shima", DisplayName: "Shima", TelegramIdentifier: "@shima"},
	}
}

// Find performs case-insensitive lookup against TelegramIdentifier. The user
// types either "@shima" or "shima" or whatever case they prefer; production
// matches on lowercased trimmed input. Returns ok=false on no match.
//
// The function is pure — passing the same identifier and the same user list
// always returns the same result.
func Find(identifier string, users []User) (User, bool) {
	needle := strings.ToLower(strings.TrimSpace(identifier))
	if needle == "" {
		return User{}, false
	}
	// Strip a leading "@" so "@shima" and "shima" are equivalent.
	needle = strings.TrimPrefix(needle, "@")

	for _, u := range users {
		hay := strings.ToLower(strings.TrimPrefix(u.TelegramIdentifier, "@"))
		if hay == needle {
			return u, true
		}
	}
	return User{}, false
}
