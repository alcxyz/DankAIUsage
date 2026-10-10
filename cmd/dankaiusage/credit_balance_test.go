package main

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestCreditBalanceLedgerRecordsTopUpsNotSpending(t *testing.T) {
	ledger := observeCreditBalance(creditBalanceLedger{}, 1000, "t0")
	ledger = observeCreditBalance(ledger, 500, "t1")
	ledger = observeCreditBalance(ledger, 1000, "t2")
	ledger = observeCreditBalance(ledger, 1000.00001, "t3")

	want := []CreditTopUp{
		{At: "t0", Amount: 1000, Before: 0},
		{At: "t2", Amount: 500, Before: 500},
	}
	if !reflect.DeepEqual(ledger.TopUps, want) || ledger.Balance != 1000.00001 || ledger.Since != "t0" || ledger.ObservedAt != "t3" {
		t.Fatalf("ledger = %+v", ledger)
	}
}

func TestCreditBalanceLedgerKeepsTheLatestTopUps(t *testing.T) {
	ledger := observeCreditBalance(creditBalanceLedger{}, 0, "start")
	for i := 1; i <= creditBalanceMaxTopUps+5; i++ {
		ledger = observeCreditBalance(ledger, float64(i), "later")
	}
	if len(ledger.TopUps) != creditBalanceMaxTopUps || ledger.TopUps[len(ledger.TopUps)-1].Before != creditBalanceMaxTopUps+4 {
		t.Fatalf("top-ups = %d, last = %+v", len(ledger.TopUps), ledger.TopUps[len(ledger.TopUps)-1])
	}
}

func TestObserveUsageHistoryAttachesBalanceHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage-history.json")
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	providersWith := func(balance float64) []ProviderUsage {
		value := balance
		limited := makeAllowance("monthly", 10, 100, time.Time{})
		return []ProviderUsage{{
			ID: "codex",
			QuotaBuckets: []QuotaBucket{
				{ID: "codex-credits", Kind: "credits", Allowance: makeUnknownAllowance("credits", time.Time{}), Balance: &value},
				{ID: "capped", Kind: "credits", Allowance: limited, Balance: &value},
			},
		}}
	}

	if _, err := observeUsageHistory(path, now, providersWith(1000)); err != nil {
		t.Fatal(err)
	}
	if _, err := observeUsageHistory(path, now.Add(time.Hour), providersWith(500)); err != nil {
		t.Fatal(err)
	}
	providers := providersWith(1000)
	if _, err := observeUsageHistory(path, now.Add(2*time.Hour), providers); err != nil {
		t.Fatal(err)
	}

	history := providers[0].QuotaBuckets[0].BalanceHistory
	if history == nil || history.Since != historyTimestamp(now) || len(history.TopUps) != 2 ||
		history.TopUps[1].Amount != 500 || history.TopUps[1].Before != 500 {
		t.Fatalf("balance history = %+v", history)
	}
	if providers[0].QuotaBuckets[1].BalanceHistory != nil {
		t.Fatal("a credit bucket with a spend limit has no top-up ledger")
	}
	state, err := loadUsageHistoryState(path)
	if err != nil || len(state.Balances["codex/codex-credits"].TopUps) != 2 {
		t.Fatalf("persisted ledger = %+v, err = %v", state.Balances, err)
	}
}
