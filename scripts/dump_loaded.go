//go:build ignore

// dump_loaded renders authenticated pages with seeded sample data so the
// design can be reviewed in its loaded (not empty) state. The script
// bypasses the handler layer and renders directly via the template
// Renderer — useful for screenshots and visual review.
//
//	go run ./scripts/dump_loaded.go home > home.html
//	go run ./scripts/dump_loaded.go transactions > transactions.html
//	go run ./scripts/dump_loaded.go spending > spending.html
//	go run ./scripts/dump_loaded.go notifications > notifications.html
//	go run ./scripts/dump_loaded.go pos > pos.html
//	go run ./scripts/dump_loaded.go verify > verify.html
package main

import (
	"fmt"
	"os"
	"time"

	tplpkg "github.com/rizaramadan/financial-shima/web/template"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: dump_loaded {home|transactions|spending|notifications|pos|verify}")
		os.Exit(2)
	}
	r := tplpkg.New()

	now := time.Date(2026, 5, 1, 14, 30, 0, 0, time.Local)
	displayName := "Riza"

	var (
		name string
		data interface{}
	)
	switch os.Args[1] {
	case "home":
		name = "home"
		data = tplpkg.HomeData{
			Title:       "Home",
			DisplayName: displayName,
			UnreadCount: 3,
			Accounts: []tplpkg.AccountRow{
				{Name: "Riza — Cash", BalanceIDR: 24_500_000},
				{Name: "Shima — BCA", BalanceIDR: 18_200_000},
				{Name: "Joint — BCA", BalanceIDR: 42_750_000},
			},
			PosByCurrency: []tplpkg.PosCurrencyGroup{
				{Currency: "IDR", Items: []tplpkg.PosRow{
					{Name: "Groceries", Cash: 3_200_000, Target: 5_000_000, HasTarget: true},
					{Name: "Mortgage", Cash: 12_000_000, Target: 12_000_000, HasTarget: true},
					{Name: "Anak Sekolah", Cash: 850_000, HasTarget: false},
					{Name: "Liburan", Cash: 7_500_000, Target: 25_000_000, HasTarget: true},
				}},
				{Currency: "USD", Items: []tplpkg.PosRow{
					{Name: "US Savings", Cash: 250_000, Target: 1_000_000, HasTarget: true},
				}},
			},
		}
	case "transactions":
		name = "transactions"
		data = tplpkg.TransactionsData{
			Title:       "Transactions",
			DisplayName: displayName,
			From:        "2026-04-01",
			To:          "2026-04-30",
			UnreadCount: 3,
			Items: []tplpkg.TransactionRow{
				{ID: "1", Type: "money_in", EffectiveDate: "2026-04-29", Amount: 25_000_000, Currency: "IDR", AccountName: "Riza — Cash", PosName: "Salary", CounterpartyName: "PT Telkom", Note: "April salary"},
				{ID: "2", Type: "money_out", EffectiveDate: "2026-04-28", Amount: 1_350_000, Currency: "IDR", AccountName: "Shima — BCA", PosName: "Groceries", CounterpartyName: "Hypermart", Note: "Weekly groceries"},
				{ID: "3", Type: "inter_pos", EffectiveDate: "2026-04-27", Amount: 5_000_000, Currency: "IDR", AccountName: "—", PosName: "Mortgage → Liburan", CounterpartyName: "—", Note: "Reallocation"},
				{ID: "4", Type: "money_out", EffectiveDate: "2026-04-26", Amount: 280_000, Currency: "IDR", AccountName: "Riza — Cash", PosName: "Anak Sekolah", CounterpartyName: "Toko Buku Gramedia", Note: "School books"},
				{ID: "5", Type: "money_out", EffectiveDate: "2026-04-25", Amount: 1_200_000, Currency: "IDR", IsReversal: true, ReversesID: "old-tx", AccountName: "Shima — BCA", PosName: "Groceries", CounterpartyName: "Hypermart", Note: "Refund — wrong charge"},
				{ID: "6", Type: "money_out", EffectiveDate: "2026-04-22", Amount: 8_500_000, Currency: "IDR", AccountName: "Joint — BCA", PosName: "Mortgage", CounterpartyName: "BCA", Note: "Monthly mortgage"},
				{ID: "7", Type: "money_in", EffectiveDate: "2026-04-15", Amount: 50_000, Currency: "USD", AccountName: "Joint — Wise USD", PosName: "US Savings", CounterpartyName: "Vendor refund", Note: "Returned subscription"},
			},
		}
	case "spending":
		name = "spending"
		cols := []tplpkg.SpendingColumn{
			{PosID: "p1", Name: "Mortgage", Currency: "IDR", Total: 51_000_000},
			{PosID: "p2", Name: "Groceries", Currency: "IDR", Total: 7_840_000},
			{PosID: "p3", Name: "Liburan", Currency: "IDR", Total: 5_000_000},
			{PosID: "p4", Name: "Anak Sekolah", Currency: "IDR", Total: 2_120_000},
			{PosID: "p5", Name: "Utilities", Currency: "IDR", Total: 1_650_000},
		}
		data = tplpkg.SpendingData{
			Title:       "Spending",
			DisplayName: displayName,
			UnreadCount: 3,
			From:        "2025-11-01", To: "2026-04-30", TopN: 5,
			Columns: cols,
			Rows: []tplpkg.SpendingRow{
				{Month: "Apr 2026", Cells: []int64{8_500_000, 1_350_000, 5_000_000, 280_000, 320_000}, Total: 15_450_000},
				{Month: "Mar 2026", Cells: []int64{8_500_000, 1_420_000, 0, 540_000, 290_000}, Total: 10_750_000},
				{Month: "Feb 2026", Cells: []int64{8_500_000, 1_180_000, 0, 320_000, 280_000}, Total: 10_280_000},
				{Month: "Jan 2026", Cells: []int64{8_500_000, 1_290_000, 0, 410_000, 270_000}, Total: 10_470_000},
				{Month: "Dec 2025", Cells: []int64{8_500_000, 1_360_000, 0, 290_000, 260_000}, Total: 10_410_000},
				{Month: "Nov 2025", Cells: []int64{8_500_000, 1_240_000, 0, 280_000, 230_000}, Total: 10_250_000},
			},
		}
	case "notifications":
		name = "notifications"
		data = tplpkg.NotificationsData{
			Title:       "Notifications",
			DisplayName: displayName,
			UnreadCount: 3,
			Items: []tplpkg.NotificationRow{
				{ID: "n1", Title: "New expense recorded", Body: "Hypermart — Rp 1.350.000 from Groceries", HasRelated: true, RelatedTxnID: "2", IsRead: false, CreatedAt: now.Add(-12 * time.Minute)},
				{ID: "n2", Title: "Mortgage repayment matched", Body: "Rp 8.500.000 from Joint — BCA cleared the obligation", HasRelated: true, RelatedTxnID: "6", IsRead: false, CreatedAt: now.Add(-3 * time.Hour)},
				{ID: "n3", Title: "Salary received", Body: "Rp 25.000.000 from PT Telkom", HasRelated: true, RelatedTxnID: "1", IsRead: false, CreatedAt: now.Add(-26 * time.Hour)},
				{ID: "n4", Title: "Refund processed", Body: "Hypermart reversed Rp 1.200.000 (wrong charge)", HasRelated: true, RelatedTxnID: "5", IsRead: true, CreatedAt: now.Add(-3 * 24 * time.Hour)},
				{ID: "n5", Title: "Liburan target progress", Body: "30% funded · Rp 7.500.000 of Rp 25.000.000", IsRead: true, CreatedAt: now.Add(-6 * 24 * time.Hour)},
			},
		}
	case "pos":
		name = "pos"
		data = tplpkg.PosDetailData{
			Title:       "Groceries",
			DisplayName: displayName,
			UnreadCount: 3,
			ID:          "p2", Name: "Groceries", Currency: "IDR",
			Target: 5_000_000, HasTarget: true,
			Receivables: 0, Payables: 1_500_000,
			Obligations: []tplpkg.ObligationRow{
				{ID: "o1", Direction: "payable", OtherPosID: "Mortgage", Currency: "IDR", Outstanding: 1_500_000, CreatedAt: now.Add(-2 * 24 * time.Hour)},
			},
			Transactions: []tplpkg.PosTransactionRow{
				{ID: "2", Type: "money_out", EffectiveDate: "2026-04-28", Amount: 1_350_000, AccountName: "Shima — BCA", CounterpartyName: "Hypermart", Note: "Weekly groceries"},
				{ID: "5", Type: "money_out", EffectiveDate: "2026-04-25", Amount: 1_200_000, IsReversal: true, ReversesID: "old", AccountName: "Shima — BCA", CounterpartyName: "Hypermart"},
				{ID: "8", Type: "money_out", EffectiveDate: "2026-04-19", Amount: 1_180_000, AccountName: "Riza — Cash", CounterpartyName: "Pasar Senen", Note: "Sayur + buah"},
				{ID: "9", Type: "money_out", EffectiveDate: "2026-04-12", Amount: 1_420_000, AccountName: "Shima — BCA", CounterpartyName: "Hypermart"},
			},
		}
	case "verify":
		name = "verify"
		data = tplpkg.VerifyData{Title: "Verify", Identifier: "@shima"}
	default:
		fmt.Fprintf(os.Stderr, "unknown page: %s\n", os.Args[1])
		os.Exit(2)
	}

	if err := r.Render(os.Stdout, name, data, nil); err != nil {
		fmt.Fprintf(os.Stderr, "render error: %v\n", err)
		os.Exit(1)
	}
}
