package incometemplate

import (
	"errors"
	"testing"
)

func tpl(lines []Line, leftover string) Template {
	t := Template{ID: "t1", Name: "Riza salary", Lines: lines}
	if leftover != "" {
		t.LeftoverPosID = leftover
		t.HasLeftoverPos = true
	}
	return t
}

func line(id, pos string, amount int64) Line {
	return Line{ID: id, PosID: pos, Amount: amount}
}

func TestApply_ExactMatch_SplitsPerLines(t *testing.T) {
	t.Parallel()
	allocs, err := Apply(tpl([]Line{
		line("l1", "mortgage", 12_000_000),
		line("l2", "groceries", 5_000_000),
		line("l3", "liburan", 3_000_000),
	}, ""), 20_000_000)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(allocs) != 3 {
		t.Fatalf("want 3 allocations, got %d", len(allocs))
	}
	if allocs[0].PosID != "mortgage" || allocs[0].Amount != 12_000_000 {
		t.Errorf("alloc[0] = %+v", allocs[0])
	}
	for _, a := range allocs {
		if a.LineID == "leftover" {
			t.Error("leftover should not appear when amount == sum")
		}
	}
}

func TestApply_AmountAboveSum_WithLeftover_AppendsRemainder(t *testing.T) {
	t.Parallel()
	allocs, err := Apply(tpl([]Line{
		line("l1", "mortgage", 12_000_000),
		line("l2", "groceries", 5_000_000),
	}, "petty-cash"), 20_000_000)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(allocs) != 3 {
		t.Fatalf("want 3 allocations (2 lines + leftover), got %d", len(allocs))
	}
	last := allocs[len(allocs)-1]
	if last.LineID != "leftover" || last.PosID != "petty-cash" || last.Amount != 3_000_000 {
		t.Errorf("leftover row wrong: %+v", last)
	}
}

func TestApply_AmountAboveSum_NoLeftover_Rejects(t *testing.T) {
	t.Parallel()
	_, err := Apply(tpl([]Line{
		line("l1", "mortgage", 12_000_000),
	}, ""), 20_000_000)
	if !errors.Is(err, ErrAmountExceedsTemplate) {
		t.Errorf("err = %v, want ErrAmountExceedsTemplate", err)
	}
}

func TestApply_AmountBelowSum_RejectsRegardlessOfLeftover(t *testing.T) {
	t.Parallel()
	for _, leftover := range []string{"", "petty-cash"} {
		_, err := Apply(tpl([]Line{
			line("l1", "mortgage", 12_000_000),
			line("l2", "groceries", 5_000_000),
		}, leftover), 10_000_000) // 10M < 17M
		if !errors.Is(err, ErrAmountBelowTemplate) {
			t.Errorf("leftover=%q: err = %v, want ErrAmountBelowTemplate", leftover, err)
		}
	}
}

func TestApply_EmptyTemplate_Rejects(t *testing.T) {
	t.Parallel()
	_, err := Apply(tpl([]Line{}, "petty-cash"), 1_000_000)
	if !errors.Is(err, ErrEmptyTemplate) {
		t.Errorf("err = %v, want ErrEmptyTemplate", err)
	}
}

func TestApply_NonPositiveAmount_Rejects(t *testing.T) {
	t.Parallel()
	for _, amt := range []int64{0, -1, -1_000_000} {
		_, err := Apply(tpl([]Line{line("l1", "x", 1000)}, ""), amt)
		if !errors.Is(err, ErrNonPositiveAmount) {
			t.Errorf("amount=%d: err = %v, want ErrNonPositiveAmount", amt, err)
		}
	}
}

func TestApply_LineIDsPreservedInOrder(t *testing.T) {
	t.Parallel()
	// Critical: the handler derives idempotency keys from LineID, so
	// the order + identity must round-trip exactly.
	allocs, err := Apply(tpl([]Line{
		line("uuid-1", "mortgage", 100),
		line("uuid-2", "groceries", 200),
		line("uuid-3", "liburan", 300),
	}, ""), 600)
	if err != nil {
		t.Fatal(err)
	}
	if allocs[0].LineID != "uuid-1" || allocs[1].LineID != "uuid-2" || allocs[2].LineID != "uuid-3" {
		t.Errorf("line IDs not preserved: %+v", allocs)
	}
}

func TestApply_SingleLineExactMatch(t *testing.T) {
	t.Parallel()
	// Edge: one-line template behaves like a regular money_in.
	allocs, err := Apply(tpl([]Line{line("l1", "savings", 1_000_000)}, ""), 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(allocs) != 1 || allocs[0].PosID != "savings" || allocs[0].Amount != 1_000_000 {
		t.Errorf("single-line: %+v", allocs)
	}
}
