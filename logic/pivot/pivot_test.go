package pivot

import (
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

// fixture mirrors the spec's worked example: two Akun would collapse
// here into one ("BSI Shima") plus a second Akun sharing a Kategori
// name ("Sampah" under both) to pin the identity rule.
func fixture() ([]Movement, []Position) {
	mov := []Movement{
		{ID: "m1", PosID: "p-hut", AccountID: "a-bsi", AccountName: "BSI Shima", PosName: "Hut Apam", Nama: "Sept 26", Date: day("2026-09-01"), Amount: 13900000, Type: "money_in"},
		{ID: "m2", PosID: "p-kon", AccountID: "a-bsi", AccountName: "BSI Shima", PosName: "Kontrakan", Nama: "Sept 26", Date: day("2026-09-01"), Amount: 95000000, Type: "money_in"},
		{ID: "m3", PosID: "p-kon", AccountID: "a-bsi", AccountName: "BSI Shima", PosName: "Kontrakan", Nama: "Okt 26", Date: day("2026-10-01"), Amount: 2500000, Type: "money_in"},
		{ID: "m4", PosID: "p-mina", AccountID: "a-bsi", AccountName: "BSI Shima", PosName: "Tab sekolah mina", Nama: "Sept 26", Date: day("2026-09-01"), Amount: 100100000, Type: "money_in"},
		{ID: "m5", PosID: "p-mina", AccountID: "a-bsi", AccountName: "BSI Shima", PosName: "Tab sekolah mina", Nama: "Okt 26", Date: day("2026-10-01"), Amount: 3000000, Type: "money_in"},
		{ID: "m6", PosID: "p-mina", AccountID: "a-bsi", AccountName: "BSI Shima", PosName: "Tab sekolah mina", Nama: "daftar SAB (23-10)", Date: day("2026-10-23"), Amount: -7350000, Type: "money_out"},
		{ID: "m7", PosID: "p-rai", AccountID: "a-bsi", AccountName: "BSI Shima", PosName: "Tab sekolah Raigan", Nama: "Sept 26", Date: day("2026-09-01"), Amount: 144952361, Type: "money_in"},
		{ID: "m8", PosID: "p-rai", AccountID: "a-bsi", AccountName: "BSI Shima", PosName: "Tab sekolah Raigan", Nama: "daftar SAB (23-10)", Date: day("2026-10-23"), Amount: -7350000, Type: "money_out"},
		{ID: "m9", PosID: "p-sampah-bsi", AccountID: "a-bsi", AccountName: "BSI Shima", PosName: "Sampah", Nama: "Sept 26", Date: day("2026-09-01"), Amount: 500000, Type: "money_in"},
		{ID: "m10", PosID: "p-sampah-dana", AccountID: "a-dana", AccountName: "Dana", PosName: "Sampah", Nama: "Sept 26", Date: day("2026-09-01"), Amount: 200000, Type: "money_in"},
	}
	pos := []Position{
		{PosID: "p-hut", AccountID: "a-bsi", AccountName: "BSI Shima", PosName: "Hut Apam"},
		{PosID: "p-kon", AccountID: "a-bsi", AccountName: "BSI Shima", PosName: "Kontrakan"},
		{PosID: "p-mina", AccountID: "a-bsi", AccountName: "BSI Shima", PosName: "Tab sekolah mina"},
		{PosID: "p-rai", AccountID: "a-bsi", AccountName: "BSI Shima", PosName: "Tab sekolah Raigan"},
		{PosID: "p-sampah-bsi", AccountID: "a-bsi", AccountName: "BSI Shima", PosName: "Sampah"},
		{PosID: "p-sampah-dana", AccountID: "a-dana", AccountName: "Dana", PosName: "Sampah"},
		{PosID: "p-empty", AccountID: "a-bsi", AccountName: "BSI Shima", PosName: "Kosong"},
	}
	return mov, pos
}

func findAkun(r Report, name string) *AkunBlock {
	for i := range r.Akuns {
		if r.Akuns[i].Name == name {
			return &r.Akuns[i]
		}
	}
	return nil
}

func findKat(ab *AkunBlock, posID string) *KategoriBlock {
	for i := range ab.Kategoris {
		if ab.Kategoris[i].PosID == posID {
			return &ab.Kategoris[i]
		}
	}
	return nil
}

// Same Kategori name under two Akun stays two separate groups.
func TestBuild_SameNameDifferentAkun_NotMerged(t *testing.T) {
	t.Parallel()
	mov, pos := fixture()
	r := Build(mov, pos, Filter{})
	if len(r.Akuns) != 2 {
		t.Fatalf("akun groups = %d, want 2", len(r.Akuns))
	}
	bsi := findAkun(r, "BSI Shima")
	dana := findAkun(r, "Dana")
	if bsi == nil || dana == nil {
		t.Fatalf("missing akun block: bsi=%v dana=%v", bsi != nil, dana != nil)
	}
	if k := findKat(bsi, "p-sampah-bsi"); k == nil || k.Subtotal != 500000 {
		t.Errorf("BSI Sampah subtotal wrong: %+v", k)
	}
	if k := findKat(dana, "p-sampah-dana"); k == nil || k.Subtotal != 200000 {
		t.Errorf("Dana Sampah subtotal wrong: %+v", k)
	}
}

// Every Kategori subtotal equals the sum of its movement rows.
func TestBuild_KategoriSubtotalEqualsRowSum(t *testing.T) {
	t.Parallel()
	mov, pos := fixture()
	r := Build(mov, pos, Filter{})
	for _, ab := range r.Akuns {
		for _, kb := range ab.Kategoris {
			var sum int64
			for _, m := range kb.Movements {
				sum += m.Amount
			}
			if kb.Subtotal != sum {
				t.Errorf("%s/%s subtotal=%d, row sum=%d", ab.Name, kb.Name, kb.Subtotal, sum)
			}
		}
	}
	bsi := findAkun(r, "BSI Shima")
	if k := findKat(bsi, "p-mina"); k.Subtotal != 100100000+3000000-7350000 {
		t.Errorf("mina subtotal=%d, want 95750000", k.Subtotal)
	}
}

// Every Akun total equals the sum of its Kategori subtotals; the grand
// total equals the sum of the Akun totals.
func TestBuild_TotalsReconcile(t *testing.T) {
	t.Parallel()
	mov, pos := fixture()
	r := Build(mov, pos, Filter{})
	var grand int64
	for _, ab := range r.Akuns {
		var sum int64
		for _, kb := range ab.Kategoris {
			sum += kb.Subtotal
		}
		if ab.Total != sum {
			t.Errorf("%s total=%d, kategori sum=%d", ab.Name, ab.Total, sum)
		}
		grand += ab.Total
	}
	if r.GrandTotal != grand {
		t.Errorf("grand=%d, akun sum=%d", r.GrandTotal, grand)
	}
	// Worked-example spot check on thekontrakan + hut apam slice.
	bsi := findAkun(r, "BSI Shima")
	if k := findKat(bsi, "p-kon"); k.Subtotal != 97500000 {
		t.Errorf("kontrakan subtotal=%d, want 97500000", k.Subtotal)
	}
}

// Movements inside a Kategori list newest first.
func TestBuild_MovementsNewestFirst(t *testing.T) {
	t.Parallel()
	mov, pos := fixture()
	r := Build(mov, pos, Filter{})
	bsi := findAkun(r, "BSI Shima")
	k := findKat(bsi, "p-mina")
	if len(k.Movements) != 3 {
		t.Fatalf("mina rows = %d, want 3", len(k.Movements))
	}
	for i := 1; i < len(k.Movements); i++ {
		if k.Movements[i-1].Date.Before(k.Movements[i].Date) {
			t.Errorf("row %d (%v) older than row %d (%v): not newest-first",
				i-1, k.Movements[i-1].Date, i, k.Movements[i].Date)
		}
	}
	if k.Movements[0].Nama != "daftar SAB (23-10)" {
		t.Errorf("first row=%q, want the Oct 23 outflow", k.Movements[0].Nama)
	}
}

// Deselecting an Akun hides its Kategori even when the Kategori is
// still selected; re-selecting restores them (filter state preserved).
func TestBuild_DeselectedAkunHidesItsKategori(t *testing.T) {
	t.Parallel()
	mov, pos := fixture()
	f := Filter{AccountIDs: map[string]bool{"a-bsi": true}, PosIDs: map[string]bool{"p-sampah-bsi": true, "p-sampah-dana": true}}
	r := Build(mov, pos, f)
	if len(r.Akuns) != 1 || r.Akuns[0].Name != "BSI Shima" {
		t.Fatalf("akun groups=%+v, want only BSI Shima", r.Akuns)
	}
	if k := findKat(&r.Akuns[0], "p-sampah-dana"); k != nil {
		t.Errorf("Dana Sampah leaked into BSI-only report")
	}
	if k := findKat(&r.Akuns[0], "p-sampah-bsi"); k == nil {
		t.Errorf("BSI Sampah missing despite selected kategori")
	}
	// Totals recompute from the filtered rows only.
	var want int64 = 500000
	if r.GrandTotal != want {
		t.Errorf("grand=%d, want %d", r.GrandTotal, want)
	}
}

// Date range, type and Nama search all narrow the rows, and every total
// recomputes from what remains.
func TestBuild_FiltersNarrowRowsAndRecompute(t *testing.T) {
	t.Parallel()
	mov, pos := fixture()
	from, to := day("2026-10-01"), day("2026-10-31")
	r := Build(mov, pos, Filter{From: &from, To: &to})
	// October rows: m3 (2.5M), m5 (3M), m6 (-7.35M), m8 (-7.35M).
	if want := int64(2500000 + 3000000 - 7350000 - 7350000); r.GrandTotal != want {
		t.Errorf("oct grand=%d, want %d", r.GrandTotal, want)
	}

	r = Build(mov, pos, Filter{Type: "money_out"})
	if want := int64(-7350000 - 7350000); r.GrandTotal != want {
		t.Errorf("out grand=%d, want %d", r.GrandTotal, want)
	}

	r = Build(mov, pos, Filter{Query: "sab"})
	if want := int64(-7350000 - 7350000); r.GrandTotal != want {
		t.Errorf("search grand=%d, want %d", r.GrandTotal, want)
	}
	if r.VisibleKat != 2 {
		t.Errorf("search kategori groups=%d, want 2", r.VisibleKat)
	}
}

// Empty positions stay hidden by default and render with a zero
// subtotal only when asked.
func TestBuild_ShowEmpty(t *testing.T) {
	t.Parallel()
	mov, pos := fixture()
	r := Build(mov, pos, Filter{})
	if findKat(findAkun(r, "BSI Shima"), "p-empty") != nil {
		t.Errorf("empty position rendered without ShowEmpty")
	}
	r = Build(mov, pos, Filter{ShowEmpty: true})
	k := findKat(findAkun(r, "BSI Shima"), "p-empty")
	if k == nil {
		t.Fatalf("empty position missing with ShowEmpty")
	}
	if k.Subtotal != 0 || len(k.Movements) != 0 {
		t.Errorf("empty kategori = subtotal %d rows %d, want 0/0", k.Subtotal, len(k.Movements))
	}
}

// Sort-by-amount orders rows largest-first instead of newest-first.
func TestBuild_SortByAmount(t *testing.T) {
	t.Parallel()
	mov, pos := fixture()
	r := Build(mov, pos, Filter{SortByAmount: true})
	k := findKat(findAkun(r, "BSI Shima"), "p-mina")
	for i := 1; i < len(k.Movements); i++ {
		if k.Movements[i-1].Amount < k.Movements[i].Amount {
			t.Errorf("row %d (%d) smaller than row %d (%d): not amount-desc",
				i-1, k.Movements[i-1].Amount, i, k.Movements[i].Amount)
		}
	}
	if r.GrandTotal == 0 {
		t.Errorf("sort must not change totals")
	}
}

func TestLabel_FallbackChain(t *testing.T) {
	t.Parallel()
	if got := Label("Sept 26", "Toko", day("2026-09-01")); got != "Sept 26" {
		t.Errorf("note wins: %q", got)
	}
	if got := Label("  ", "Toko", day("2026-09-01")); got != "Toko" {
		t.Errorf("counterparty fallback: %q", got)
	}
	if got := Label("", "", day("2026-09-01")); got != "2026-09-01" {
		t.Errorf("date fallback: %q", got)
	}
}
