package incometemplate

import (
	"errors"
	"math/rand"
	"testing"
)

func pline(id, pos string, amount int64, bps int) Line {
	return Line{ID: id, PosID: pos, Amount: amount, PercentBps: bps}
}

func TestApply_PercentOnly_ExactSplit(t *testing.T) {
	t.Parallel()
	allocs, err := Apply(Template{ID: "t1", Lines: []Line{
		pline("l1", "a", 0, 6000),
		pline("l2", "b", 0, 4000),
	}}, 10_000_000)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(allocs) != 2 {
		t.Fatalf("want 2 allocations, got %d", len(allocs))
	}
	if allocs[0].Amount != 6_000_000 || allocs[1].Amount != 4_000_000 {
		t.Errorf("allocs = %+v", allocs)
	}
	var sum int64
	for _, a := range allocs {
		sum += a.Amount
	}
	if sum != 10_000_000 {
		t.Errorf("sum = %d, want 10000000", sum)
	}
}

func TestApply_Percent_RoundingLargestRemainder(t *testing.T) {
	t.Parallel()
	// 3 equal 3333bps shares of 100 → 33/33/34 in some order; sum exact.
	allocs, err := Apply(Template{ID: "t1", Lines: []Line{
		pline("l1", "a", 0, 3333),
		pline("l2", "b", 0, 3333),
		pline("l3", "c", 0, 3334),
	}}, 100)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var sum int64
	for _, a := range allocs {
		sum += a.Amount
	}
	if sum != 100 {
		t.Errorf("sum = %d, want 100", sum)
	}
	for _, a := range allocs {
		if a.Amount < 33 || a.Amount > 34 {
			t.Errorf("rounding >1 unit: %+v", a)
		}
	}
}

func TestApply_PercentTotalExceeds_Rejects(t *testing.T) {
	t.Parallel()
	_, err := Apply(Template{ID: "t1", Lines: []Line{
		pline("l1", "a", 0, 6000),
		pline("l2", "b", 0, 5000),
	}}, 1_000_000)
	if !errors.Is(err, ErrPercentTotalExceeds) {
		t.Errorf("err = %v, want ErrPercentTotalExceeds", err)
	}
}

func TestApply_PercentPartial_WithoutLeftover_Rejects(t *testing.T) {
	t.Parallel()
	_, err := Apply(Template{ID: "t1", Lines: []Line{
		pline("l1", "a", 0, 6000),
	}}, 1_000_000)
	if !errors.Is(err, ErrAmountExceedsTemplate) {
		t.Errorf("err = %v, want ErrAmountExceedsTemplate", err)
	}
}

func TestApply_PercentPartial_WithLeftover_Remainder(t *testing.T) {
	t.Parallel()
	tmpl := Template{ID: "t1", Lines: []Line{pline("l1", "a", 0, 6000)}}
	tmpl.LeftoverPosID = "lp"
	tmpl.HasLeftoverPos = true
	allocs, err := Apply(tmpl, 1_000_000)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(allocs) != 2 {
		t.Fatalf("want 2 allocations, got %d", len(allocs))
	}
	if allocs[0].Amount != 600_000 {
		t.Errorf("percent line = %d, want 600000", allocs[0].Amount)
	}
	if allocs[1].LineID != "leftover" || allocs[1].Amount != 400_000 {
		t.Errorf("leftover = %+v", allocs[1])
	}
}

func TestApply_MixedFixedPercent(t *testing.T) {
	t.Parallel()
	tmpl := Template{ID: "t1", Lines: []Line{
		pline("l1", "fixed-pos", 200_000, 0),
		pline("l2", "pct-pos", 0, 5000),
	}}
	tmpl.LeftoverPosID = "lp"
	tmpl.HasLeftoverPos = true
	allocs, err := Apply(tmpl, 1_000_000)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var sum int64
	for _, a := range allocs {
		sum += a.Amount
	}
	if sum != 1_000_000 {
		t.Errorf("sum = %d, want 1000000", sum)
	}
	if allocs[0].Amount != 200_000 || allocs[1].Amount != 500_000 {
		t.Errorf("allocs = %+v", allocs)
	}
	if allocs[2].LineID != "leftover" || allocs[2].Amount != 300_000 {
		t.Errorf("leftover = %+v", allocs[2])
	}
}

func TestApply_MixedFixedPercent_BelowFixed_Rejects(t *testing.T) {
	t.Parallel()
	_, err := Apply(Template{ID: "t1", Lines: []Line{
		pline("l1", "fixed-pos", 900_000, 0),
		pline("l2", "pct-pos", 0, 5000),
	}}, 1_000_000) // 900k + 500k demand > 1000k
	if !errors.Is(err, ErrAmountBelowTemplate) {
		t.Errorf("err = %v, want ErrAmountBelowTemplate", err)
	}
}

func TestApply_InvalidPercent_Rejects(t *testing.T) {
	t.Parallel()
	for _, bps := range []int{-1, -100, 10001, 20000} {
		_, err := Apply(Template{ID: "t1", Lines: []Line{
			pline("l1", "a", 0, bps),
		}}, 1_000_000)
		if !errors.Is(err, ErrInvalidPercent) {
			t.Errorf("bps=%d: err = %v, want ErrInvalidPercent", bps, err)
		}
	}
}

func TestApply_LineWithNeitherFixedNorPercent_Rejects(t *testing.T) {
	t.Parallel()
	_, err := Apply(Template{ID: "t1", Lines: []Line{
		pline("l1", "a", 0, 0),
	}}, 1_000_000)
	if !errors.Is(err, ErrInvalidLine) {
		t.Errorf("err = %v, want ErrInvalidLine", err)
	}
}

// TestApply_SumInvariant_Property: whenever Apply succeeds,
// Σ(allocations) == amount (fixed, percent, and mixed templates),
// and every percent line is within 1 unit of its exact share.
func TestApply_SumInvariant_Property(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(42))
	shapes := [][]Line{
		{pline("l1", "a", 300_000, 0), pline("l2", "b", 200_000, 0)},
		{pline("l1", "a", 0, 6000), pline("l2", "b", 0, 4000)},
		{pline("l1", "a", 0, 3333), pline("l2", "b", 0, 3333), pline("l3", "c", 0, 3334)},
		{pline("l1", "a", 150_000, 0), pline("l2", "b", 0, 5000), pline("l3", "c", 50_000, 2000)},
		{pline("l1", "a", 0, 1), pline("l2", "b", 0, 9999)},
	}
	for i := 0; i < 200; i++ {
		shape := shapes[rng.Intn(len(shapes))]
		amount := int64(1+rng.Intn(50_000_000)) * 100
		tmpl := Template{ID: "t", Lines: shape, LeftoverPosID: "lp", HasLeftoverPos: true}
		allocs, err := Apply(tmpl, amount)
		if err != nil {
			t.Fatalf("iter %d amount %d: err %v", i, amount, err)
		}
		var sum int64
		for _, a := range allocs {
			sum += a.Amount
		}
		if sum != amount {
			t.Fatalf("iter %d amount %d: sum = %d", i, amount, sum)
		}
		// Rounding bound: each line's percent part within 1 of exact.
		for li, l := range shape {
			if l.PercentBps == 0 {
				continue
			}
			exact := float64(l.PercentBps) * float64(amount) / 10000
			got := float64(allocs[li].Amount - l.Amount)
			if d := got - exact; d > 1 || d < -1 {
				t.Fatalf("iter %d line %s: got %v exact %v (amount %d)",
					i, l.ID, got, exact, amount)
			}
		}
	}
}
