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

// observeCreditBalances updates the ledger of every prepaid balance without a
// spend limit and attaches it to the bucket. Any increase counts as a top-up;
// decreases are spending. Providers share their bucket slices with the caller,
// so the attached history reaches the summary.
func observeCreditBalances(state *usageHistoryState, providers []ProviderUsage, now time.Time) {
	if state.Balances == nil {
		state.Balances = map[string]creditBalanceLedger{}
	}
	at := historyTimestamp(now)
	for p := range providers {
		buckets := providers[p].QuotaBuckets
		for b := range buckets {
			bucket := &buckets[b]
			if bucket.Kind != "credits" || bucket.Allowance.Known || bucket.Balance == nil || *bucket.Balance < 0 {
				continue
			}
			key := providers[p].ID + "/" + bucket.ID
			ledger := observeCreditBalance(state.Balances[key], *bucket.Balance, at)
			state.Balances[key] = ledger
			bucket.BalanceHistory = &CreditBalanceHistory{
				Since:  ledger.Since,
				TopUps: append([]CreditTopUp{}, ledger.TopUps...),
			}
		}
	}
}

func observeCreditBalance(ledger creditBalanceLedger, balance float64, at string) creditBalanceLedger {
	balance = roundCredit(balance)
	if ledger.Since == "" || len(ledger.TopUps) == 0 {
		return creditBalanceLedger{
			Balance:    balance,
			ObservedAt: at,
			Since:      at,
			TopUps:     []CreditTopUp{{At: at, Amount: balance, Before: 0}},
		}
	}
	if balance > ledger.Balance+creditBalanceEpsilon {
		ledger.TopUps = append(ledger.TopUps, CreditTopUp{
			At:     at,
			Amount: roundCredit(balance - ledger.Balance),
			Before: ledger.Balance,
		})
		if len(ledger.TopUps) > creditBalanceMaxTopUps {
			ledger.TopUps = append([]CreditTopUp(nil), ledger.TopUps[len(ledger.TopUps)-creditBalanceMaxTopUps:]...)
		}
	}
	ledger.Balance = balance
	ledger.ObservedAt = at
	return ledger
}

func roundCredit(value float64) float64 {
	return math.Round(value*1e6) / 1e6
}
