// Package pivot builds the roll-forward report: movements grouped by
// (Akun, Kategori), subtotalled per Kategori and per Akun, with a grand
// total. Pure: no DB, no clock. The handler feeds it DB rows; the report
// it returns is rendered by the pivot template and its client-side filter.
package pivot

import (
	"sort"
	"strings"
	"time"
)

// Movement is one dated amount inside a Kategori. Amount is signed:
// money_in positive, money_out negative. Type is the transaction type
// ("money_in" | "money_out").
type Movement struct {
	ID          string
	PosID       string
	AccountID   string
	AccountName string
	PosName     string
	Nama        string
	Date        time.Time
	Amount      int64
	Type        string
}

// Position is one (Akun, Kategori) identity. It exists so positions with
// no movements in the current selection can still render when the user
// asks to see them (ShowEmpty).
type Position struct {
	PosID       string
	AccountID   string
	AccountName string
	PosName     string
}

// Filter selects which movements enter the report. A nil or empty
// AccountIDs / PosIDs set means "all selected". From / To are inclusive
// day bounds; nil means unbounded. Type is "" (or "all"), "money_in" or
// "money_out". Query is a case-insensitive substring match on Nama.
type Filter struct {
	AccountIDs   map[string]bool
	PosIDs       map[string]bool
	From         *time.Time
	To           *time.Time
	Type         string
	Query        string
	SortByAmount bool
	ShowEmpty    bool
}

// KategoriBlock is one position's movements plus its subtotal.
type KategoriBlock struct {
	PosID     string
	Name      string
	Movements []Movement
	Subtotal  int64
}

// AkunBlock is one account's positions plus its total.
type AkunBlock struct {
	AccountID string
	Name      string
	Kategoris []KategoriBlock
	Total     int64
}

// Report is the grouped roll-forward. GrandTotal always equals the sum
// of the Akun totals, each of which equals the sum of its Kategori
// subtotals, each of which equals the sum of its movement rows.
type Report struct {
	Akuns       []AkunBlock
	GrandTotal  int64
	VisibleAkun int // akun groups rendered
	VisibleKat  int // kategori groups rendered
}

// Label picks the Nama for a movement: the note when present, else the
// counterparty name, else the effective date. It never returns empty so
// every movement row has a label to search and display.
func Label(note, counterparty string, date time.Time) string {
	if s := strings.TrimSpace(note); s != "" {
		return s
	}
	if s := strings.TrimSpace(counterparty); s != "" {
		return s
	}
	return date.Format("2006-01-02")
}

// Build groups movements by (AccountID, PosID) under the filter and
// recomputes every subtotal from the filtered rows. Akun blocks sort by
// name, Kategori blocks by name; movements sort newest-first by date (or
// by amount when the filter asks).
func Build(movements []Movement, positions []Position, f Filter) Report {
	var out Report
	query := strings.ToLower(strings.TrimSpace(f.Query))

	type katKey struct{ akun, pos string }
	byKat := map[katKey]*KategoriBlock{}
	akunName := map[string]string{}
	katOrder := map[katKey][]Movement{}

	for _, m := range movements {
		if len(f.AccountIDs) > 0 && !f.AccountIDs[m.AccountID] {
			continue
		}
		if len(f.PosIDs) > 0 && !f.PosIDs[m.PosID] {
			continue
		}
		if f.From != nil && m.Date.Before(*f.From) {
			continue
		}
		if f.To != nil && m.Date.After(*f.To) {
			continue
		}
		if t := strings.TrimSpace(f.Type); t != "" && t != "all" && m.Type != t {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(m.Nama), query) {
			continue
		}
		akunName[m.AccountID] = m.AccountName
		k := katKey{m.AccountID, m.PosID}
		if _, ok := byKat[k]; !ok {
			byKat[k] = &KategoriBlock{PosID: m.PosID, Name: m.PosName}
		}
		katOrder[k] = append(katOrder[k], m)
	}

	// Positions with no movements render only on request. A deselected
	// Akun hides its Kategori unconditionally, even when the Kategori
	// itself is still selected.
	if f.ShowEmpty {
		for _, p := range positions {
			if len(f.AccountIDs) > 0 && !f.AccountIDs[p.AccountID] {
				continue
			}
			if len(f.PosIDs) > 0 && !f.PosIDs[p.PosID] {
				continue
			}
			akunName[p.AccountID] = p.AccountName
			k := katKey{p.AccountID, p.PosID}
			if _, ok := byKat[k]; !ok {
				byKat[k] = &KategoriBlock{PosID: p.PosID, Name: p.PosName}
			}
		}
	}

	byAkun := map[string]*AkunBlock{}
	for k, kb := range byKat {
		rows := katOrder[k]
		if f.SortByAmount {
			sort.SliceStable(rows, func(i, j int) bool {
				if rows[i].Amount != rows[j].Amount {
					return rows[i].Amount > rows[j].Amount
				}
				return rows[i].Date.After(rows[j].Date)
			})
		} else {
			sort.SliceStable(rows, func(i, j int) bool {
				if !rows[i].Date.Equal(rows[j].Date) {
					return rows[i].Date.After(rows[j].Date)
				}
				return rows[i].Nama < rows[j].Nama
			})
		}
		kb.Movements = rows
		for _, m := range rows {
			kb.Subtotal += m.Amount
		}
		ab, ok := byAkun[k.akun]
		if !ok {
			ab = &AkunBlock{AccountID: k.akun, Name: akunName[k.akun]}
			byAkun[k.akun] = ab
		}
		ab.Kategoris = append(ab.Kategoris, *kb)
	}

	for _, ab := range byAkun {
		sort.SliceStable(ab.Kategoris, func(i, j int) bool {
			if ab.Kategoris[i].Name != ab.Kategoris[j].Name {
				return ab.Kategoris[i].Name < ab.Kategoris[j].Name
			}
			return ab.Kategoris[i].PosID < ab.Kategoris[j].PosID
		})
		for _, kb := range ab.Kategoris {
			ab.Total += kb.Subtotal
		}
		out.Akuns = append(out.Akuns, *ab)
	}
	sort.SliceStable(out.Akuns, func(i, j int) bool {
		if out.Akuns[i].Name != out.Akuns[j].Name {
			return out.Akuns[i].Name < out.Akuns[j].Name
		}
		return out.Akuns[i].AccountID < out.Akuns[j].AccountID
	})
	for _, ab := range out.Akuns {
		out.GrandTotal += ab.Total
		out.VisibleAkun++
		out.VisibleKat += len(ab.Kategoris)
	}
	return out
}
