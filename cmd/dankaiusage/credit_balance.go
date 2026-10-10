package main

import (
	"math"
	"time"
)

// creditBalanceMaxTopUps bounds the ledger; top-ups are rare, so this covers
// far more than the longest token range.
const creditBalanceMaxTopUps = 64

// creditBalanceEpsilon ignores rounding noise below a hundredth of a cent.
const creditBalanceEpsilon = 0.0001

// CreditTopUp is one observed balance increase. The first entry of a ledger is
// the balance when the helper first saw it, recorded as a top-up from zero.
type CreditTopUp struct {
	At     string  `json:"at"`
	Amount float64 `json:"amount"`
	Before float64 `json:"before"`
}

type CreditBalanceHistory struct {
	Since  string        `json:"since"`
	TopUps []CreditTopUp `json:"topUps"`
}

type creditBalanceLedger struct {
	Balance    float64       `json:"balance"`
	ObservedAt string        `json:"observedAt"`
	Since      string        `json:"since"`
	TopUps     []CreditTopUp `json:"topUps"`
}

// emptyCreditBucketMeta names a credit bucket the provider reported as empty
// and therefore omitted from the summary.
const emptyCreditBucketMeta = "emptyCreditBucket"

// observeCreditBalances updates the ledger of every prepaid balance without a
// spend limit and attaches it to the bucket. Any increase counts as a top-up;
// decreases are spending. Only fresh snapshots newer than the ledger update it,
// so an older cached snapshot cannot fake a top-up. Providers share their
// bucket slices with the caller, so the attached history reaches the summary.
func observeCreditBalances(state *usageHistoryState, providers []ProviderUsage, now time.Time) {
	if state.Balances == nil {
		state.Balances = map[string]creditBalanceLedger{}
	}
	for p := range providers {
		provider := &providers[p]
		fresh := providerHistoryIsFresh(*provider)
		observedAt := now
		if !provider.historyObservedAt.IsZero() {
			observedAt = provider.historyObservedAt
		}
		if empty := stringValue(provider.Meta[emptyCreditBucketMeta]); fresh && empty != "" {
			key := provider.ID + "/" + empty
			if ledger, ok := state.Balances[key]; ok {
				state.Balances[key] = observeCreditBalance(ledger, 0, observedAt)
			}
		}
		for b := range provider.QuotaBuckets {
			bucket := &provider.QuotaBuckets[b]
			if bucket.Kind != "credits" || bucket.Allowance.Known || bucket.Balance == nil || *bucket.Balance < 0 {
				continue
			}
			key := provider.ID + "/" + bucket.ID
			ledger, ok := state.Balances[key]
			if fresh {
				ledger = observeCreditBalance(ledger, *bucket.Balance, observedAt)
				state.Balances[key] = ledger
			} else if !ok {
				continue
			}
			bucket.BalanceHistory = &CreditBalanceHistory{
				Since:  ledger.Since,
				TopUps: append([]CreditTopUp{}, ledger.TopUps...),
			}
		}
	}
}

func observeCreditBalance(ledger creditBalanceLedger, balance float64, observedAt time.Time) creditBalanceLedger {
	balance = roundCredit(balance)
	at := historyTimestamp(observedAt)
	if ledger.Since == "" || len(ledger.TopUps) == 0 {
		return creditBalanceLedger{
			Balance:    balance,
			ObservedAt: at,
			Since:      at,
			TopUps:     []CreditTopUp{{At: at, Amount: balance, Before: 0}},
		}
	}
	if previous, err := time.Parse(time.RFC3339Nano, ledger.ObservedAt); err == nil && observedAt.Before(previous) {
		return ledger
	}
	switch {
	case balance > ledger.Balance+creditBalanceEpsilon:
		ledger.TopUps = append(ledger.TopUps, CreditTopUp{
			At:     at,
			Amount: roundCredit(balance - ledger.Balance),
			Before: ledger.Balance,
		})
		if len(ledger.TopUps) > creditBalanceMaxTopUps {
			ledger.TopUps = append([]CreditTopUp(nil), ledger.TopUps[len(ledger.TopUps)-creditBalanceMaxTopUps:]...)
		}
		ledger.Balance = balance
	case balance < ledger.Balance:
		ledger.Balance = balance
	}
	// A rise within the epsilon keeps the lower base, so repeated tiny
	// increases still add up to a recorded top-up.
	ledger.ObservedAt = at
	return ledger
}

func roundCredit(value float64) float64 {
	return math.Round(value*1e6) / 1e6
}
