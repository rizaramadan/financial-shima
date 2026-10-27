package obligation

// Outstanding returns the unpaid remainder of one open obligation in
// cents: owed - repaid. Pure — GET /api/v1/debts renders this per row
// instead of recomputing inline in the handler.
func Outstanding(o Obligation) int64 { return o.Owed - o.Repaid }

// TotalsByCurrency sums outstanding per currency across open
// obligations. Pure — the handler's per-currency rollup for
// GET /api/v1/debts. Returns an empty (non-nil) map on empty input so
// callers can range without a nil check.
func TotalsByCurrency(obs []Obligation) map[string]int64 {
	out := map[string]int64{}
	for _, o := range obs {
		out[o.Currency] += Outstanding(o)
	}
	return out
}
