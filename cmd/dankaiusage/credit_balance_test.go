package main

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

var creditTestStart = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func creditTestAt(hours int) time.Time {
	return creditTestStart.Add(time.Duration(hours) * time.Hour)
}

func TestCreditBalanceLedgerRecordsTopUpsNotSpending(t *testing.T) {
	ledger := observeCreditBalance(creditBalanceLedger{}, 1000, creditTestAt(0))
	ledger = observeCreditBalance(ledger, 500, creditTestAt(1))
	ledger = observeCreditBalance(ledger, 1000, creditTestAt(2))

	want := []CreditTopUp{
		{At: historyTimestamp(creditTestAt(0)), Amount: 1000, Before: 0},
		{At: historyTimestamp(creditTestAt(2)), Amount: 500, Before: 500},
	}
	if !reflect.DeepEqual(ledger.TopUps, want) || ledger.Balance != 1000 || ledger.Since != historyTimestamp(creditTestAt(0)) {
		t.Fatalf("ledger = %+v", ledger)
	}
}

func TestCreditBalanceLedgerIgnoresOlderSnapshots(t *testing.T) {
	ledger := observeCreditBalance(creditBalanceLedger{}, 100, creditTestAt(0))
	ledger = observeCreditBalance(ledger, 80, creditTestAt(2))
	ledger = observeCreditBalance(ledger, 100, creditTestAt(1))
	ledger = observeCreditBalance(ledger, 80, creditTestAt(3))
	if len(ledger.TopUps) != 1 || ledger.Balance != 80 {
		t.Fatalf("an older snapshot faked a top-up: %+v", ledger)
	}
}

func TestCreditBalanceLedgerAddsUpTinyIncreases(t *testing.T) {
	ledger := observeCreditBalance(creditBalanceLedger{}, 10, creditTestAt(0))
	for i := 1; i <= 3; i++ {
		ledger = observeCreditBalance(ledger, 10+float64(i)*0.00006, creditTestAt(i))
	}
	if len(ledger.TopUps) != 2 || ledger.TopUps[1].Before != 10 {
		t.Fatalf("tiny increases were lost: %+v", ledger)
	}
}

func TestCreditBalanceLedgerKeepsTheLatestTopUps(t *testing.T) {
	ledger := observeCreditBalance(creditBalanceLedger{}, 0, creditTestAt(0))
	for i := 1; i <= creditBalanceMaxTopUps+5; i++ {
		ledger = observeCreditBalance(ledger, float64(i), creditTestAt(i))
	}
	if len(ledger.TopUps) != creditBalanceMaxTopUps || ledger.TopUps[len(ledger.TopUps)-1].Before != creditBalanceMaxTopUps+4 {
		t.Fatalf("top-ups = %d, last = %+v", len(ledger.TopUps), ledger.TopUps[len(ledger.TopUps)-1])
	}
}

func creditTestProviders(observedAt time.Time, balance *float64, fresh bool) []ProviderUsage {
	meta := map[string]any{}
	if !fresh {
		meta["usageDataStale"] = true
	}
	provider := ProviderUsage{ID: "codex", Available: true, Meta: meta, historyObservedAt: observedAt}
	limited := 5.0
	provider.QuotaBuckets = []QuotaBucket{
		{ID: "capped", Kind: "credits", Allowance: makeAllowance("monthly", 10, 100, time.Time{}), Balance: &limited},
	}
	if balance != nil {
		provider.QuotaBuckets = append(provider.QuotaBuckets,
			QuotaBucket{ID: "codex-credits", Kind: "credits", Allowance: makeUnknownAllowance("credits", time.Time{}), Balance: balance})
	} else {
		meta[emptyCreditBucketMeta] = "codex-credits"
	}
	return []ProviderUsage{provider}
}

func TestObserveUsageHistoryAttachesBalanceHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage-history.json")
	value := func(v float64) *float64 { return &v }
	observe := func(providers []ProviderUsage) []ProviderUsage {
		if _, err := observeUsageHistory(path, creditTestAt(10), providers); err != nil {
			t.Fatal(err)
		}
		return providers
	}

	observe(creditTestProviders(creditTestAt(0), value(100), true))
	// An emptied balance hides the bucket but still reaches the ledger.
	observe(creditTestProviders(creditTestAt(1), nil, true))
	// A stale snapshot shows the ledger without changing it.
	stale := observe(creditTestProviders(creditTestAt(2), value(70), false))
	if history := stale[0].QuotaBuckets[1].BalanceHistory; history == nil || len(history.TopUps) != 1 {
		t.Fatalf("stale snapshot history = %+v", history)
	}
	providers := observe(creditTestProviders(creditTestAt(3), value(50), true))

	history := providers[0].QuotaBuckets[1].BalanceHistory
	if history == nil || history.Since != historyTimestamp(creditTestAt(0)) || len(history.TopUps) != 2 ||
		history.TopUps[1].Amount != 50 || history.TopUps[1].Before != 0 {
		t.Fatalf("balance history = %+v", history)
	}
	if providers[0].QuotaBuckets[0].BalanceHistory != nil {
		t.Fatal("a credit bucket with a spend limit has no top-up ledger")
	}
	state, err := loadUsageHistoryState(path)
	if err != nil || state.Balances["codex/codex-credits"].Balance != 50 {
		t.Fatalf("persisted ledger = %+v, err = %v", state.Balances, err)
	}
}
