package engine

import (
	"math/bits"
	"sort"

	"github.com/universaltill/ut-plugin-page-appointments/src/money"
)

// MaxTotalMinor is the largest total the engine accepts (10^12 minor units).
const MaxTotalMinor = 1_000_000_000_000

// Total sums the line totals. A negative line is ErrInvalid; a total above
// MaxTotalMinor is ErrAmountTooLarge.
func Total(lines []Line) (money.Money, error) {
	var sum money.Money
	for _, l := range lines {
		if l.Amount.IsNegative() {
			return 0, ErrInvalid
		}
		if l.Amount > MaxTotalMinor {
			return 0, ErrAmountTooLarge
		}
		sum = sum.Add(l.Amount)
		if sum > MaxTotalMinor {
			return 0, ErrAmountTooLarge
		}
	}
	return sum, nil
}

// ValidateDeposit checks 0 <= deposit <= total (0 means no deposit).
func ValidateDeposit(total, deposit money.Money) error {
	if deposit.IsNegative() {
		return ErrDepositInvalid
	}
	if total > MaxTotalMinor {
		return ErrAmountTooLarge
	}
	if deposit.IsZero() {
		return nil
	}
	if deposit > total {
		return ErrDepositTooLarge
	}
	return nil
}

// AllocateDeposit spreads deposit over the lines: floor(amount*deposit/total)
// each (exact 128-bit arithmetic), the leftover units going one each to the
// largest fractional remainders, ties to the lower line index. The allocations
// sum to deposit exactly and never exceed their line. remainder[i] is the line
// amount left to pay.
func AllocateDeposit(lines []Line, deposit money.Money) (alloc, remainder []money.Money, err error) {
	total, err := Total(lines)
	if err != nil {
		return nil, nil, err
	}
	if err := ValidateDeposit(total, deposit); err != nil {
		return nil, nil, err
	}
	n := len(lines)
	alloc = make([]money.Money, n)
	remainder = make([]money.Money, n)
	if deposit.IsZero() {
		for i, l := range lines {
			remainder[i] = l.Amount
		}
		return alloc, remainder, nil
	}
	rems := make([]uint64, n)
	var given uint64
	for i, l := range lines {
		// amount <= total, so hi < total and Div64 cannot panic.
		hi, lo := bits.Mul64(uint64(l.Amount), uint64(deposit))
		q, r := bits.Div64(hi, lo, uint64(total))
		alloc[i] = money.FromMinor(int64(q))
		rems[i] = r
		given += q
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return rems[idx[a]] > rems[idx[b]] })
	for k := uint64(0); k < uint64(deposit)-given; k++ {
		alloc[idx[k]]++
	}
	for i, l := range lines {
		remainder[i] = l.Amount.Sub(alloc[i])
	}
	return alloc, remainder, nil
}

// Applied is the checkout application of a deposit.
type Applied struct{ Applied, Excess, Remainder money.Money }

// ApplyDeposit applies a deposit to a basket: Applied = min(deposit, basket),
// the rest of the deposit is reported as Excess (refunds are out of scope).
func ApplyDeposit(basketTotal, deposit money.Money) Applied {
	if basketTotal.IsNegative() {
		basketTotal = 0
	}
	if deposit.IsNegative() {
		deposit = 0
	}
	applied := deposit
	if basketTotal < applied {
		applied = basketTotal
	}
	return Applied{Applied: applied, Excess: deposit.Sub(applied), Remainder: basketTotal.Sub(applied)}
}
