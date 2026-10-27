package obligation

import (
	"testing"
)

// TDD: Outstanding + TotalsByCurrency drive GET /api/v1/debts.
// outstanding = owed - repaid (cents, int64); totals group by currency.
func TestOutstanding_FullAndPartial(t *testing.T) {
	t.Parallel()
	full := Obligation{ID: "o1", CreditorPosID: "C", DebtorPosID: "D",
		Currency: "idr", Owed: 1_000_000, Repaid: 0}
	if got := Outstanding(full); got != 1_000_000 {
		t.Errorf("Outstanding = %d, want 1000000", got)
	}
	partial := full
	partial.Repaid = 400_000
	if got := Outstanding(partial); got != 600_000 {
		t.Errorf("Outstanding partial = %d, want 600000", got)
	}
}

func TestTotalsByCurrency_GroupsOutstanding(t *testing.T) {
	t.Parallel()
	obs := []Obligation{
		{ID: "o1", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr", Owed: 1000, Repaid: 200},
		{ID: "o2", CreditorPosID: "C", DebtorPosID: "E", Currency: "idr", Owed: 500, Repaid: 0},
		{ID: "o3", CreditorPosID: "G", DebtorPosID: "H", Currency: "gold-g", Owed: 5, Repaid: 4},
	}
	got := TotalsByCurrency(obs)
	if got["idr"] != 1300 {
		t.Errorf(`totals["idr"] = %d, want 1300`, got["idr"])
	}
	if got["gold-g"] != 1 {
		t.Errorf(`totals["gold-g"] = %d, want 1`, got["gold-g"])
	}
	if len(got) != 2 {
		t.Errorf("len(totals) = %d, want 2", len(got))
	}
}

func TestTotalsByCurrency_Empty(t *testing.T) {
	t.Parallel()
	if got := TotalsByCurrency(nil); len(got) != 0 {
		t.Errorf("empty input: got %v, want empty", got)
	}
}
