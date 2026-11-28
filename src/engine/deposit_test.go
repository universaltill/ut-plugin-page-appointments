package engine

import (
	"errors"
	"math/big"
	"math/rand"
	"reflect"
	"testing"

	"github.com/universaltill/ut-plugin-page-appointments/src/money"
)

func lines(amts ...int64) []Line {
	out := make([]Line, len(amts))
	for i, a := range amts {
		out[i] = Line{SKU: "s", Qty: 1, Amount: money.FromMinor(a)}
	}
	return out
}

func minors(ms []money.Money) []int64 {
	out := make([]int64, len(ms))
	for i, m := range ms {
		out[i] = m.Minor()
	}
	return out
}

func TestDeposit_Total(t *testing.T) {
	if got, err := Total(lines(100, 250)); err != nil || got != 350 {
		t.Fatal(got, err)
	}
	if _, err := Total(lines(100, -1)); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if got, err := Total(nil); err != nil || got != 0 {
		t.Fatal(got, err)
	}
	if _, err := Total(lines(MaxTotalMinor, 1)); !errors.Is(err, ErrAmountTooLarge) {
		t.Fatal(err)
	}
	if _, err := Total(lines(MaxTotalMinor + 1)); !errors.Is(err, ErrAmountTooLarge) {
		t.Fatal(err)
	}
	if got, err := Total(lines(MaxTotalMinor)); err != nil || got != MaxTotalMinor {
		t.Fatal(got, err)
	}
}

func TestDeposit_Validate(t *testing.T) {
	cases := []struct {
		total, dep int64
		want       error
	}{
		{3000, 3000, nil}, {3000, 3001, ErrDepositTooLarge}, {3000, 0, nil},
		{3000, -1, ErrDepositInvalid}, {0, 1, ErrDepositTooLarge}, {MaxTotalMinor + 1, 5, ErrAmountTooLarge},
	}
	for _, c := range cases {
		if err := ValidateDeposit(money.FromMinor(c.total), money.FromMinor(c.dep)); !errors.Is(err, c.want) {
			t.Errorf("%d/%d: %v want %v", c.total, c.dep, err, c.want)
		}
	}
}

func alloc(t *testing.T, dep int64, amts ...int64) ([]int64, []int64) {
	t.Helper()
	a, r, err := AllocateDeposit(lines(amts...), money.FromMinor(dep))
	if err != nil {
		t.Fatal(err)
	}
	return minors(a), minors(r)
}

func TestDeposit_VectorA(t *testing.T) {
	a, r := alloc(t, 1000, 3333, 3333, 3334)
	if !reflect.DeepEqual(a, []int64{333, 333, 334}) || !reflect.DeepEqual(r, []int64{3000, 3000, 3000}) {
		t.Fatal(a, r)
	}
}

func TestDeposit_VectorB(t *testing.T) {
	a, r := alloc(t, 2, 1, 1, 1)
	if !reflect.DeepEqual(a, []int64{1, 1, 0}) || !reflect.DeepEqual(r, []int64{0, 0, 1}) {
		t.Fatal(a, r)
	}
}

func TestDeposit_VectorC(t *testing.T) {
	a, r := alloc(t, 7333, 1999, 2001, 3333)
	if !reflect.DeepEqual(a, []int64{1999, 2001, 3333}) || !reflect.DeepEqual(r, []int64{0, 0, 0}) {
		t.Fatal(a, r)
	}
	a, _ = alloc(t, 1, 1999, 2001, 3333)
	if !reflect.DeepEqual(a, []int64{0, 0, 1}) {
		t.Fatal(a)
	}
	a, r = alloc(t, 0, 5, 6)
	if !reflect.DeepEqual(a, []int64{0, 0}) || !reflect.DeepEqual(r, []int64{5, 6}) {
		t.Fatal(a, r)
	}
}

func TestDeposit_Errors(t *testing.T) {
	if _, _, err := AllocateDeposit(lines(100, 100), 201); !errors.Is(err, ErrDepositTooLarge) {
		t.Fatal(err)
	}
	if _, _, err := AllocateDeposit(lines(100, -1), 10); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, _, err := AllocateDeposit(lines(MaxTotalMinor, 1), 10); !errors.Is(err, ErrAmountTooLarge) {
		t.Fatal(err)
	}
}

func TestDeposit_TooLarge(t *testing.T) {
	if _, _, err := AllocateDeposit(lines(MaxTotalMinor+1), 5); !errors.Is(err, ErrAmountTooLarge) {
		t.Fatal(err)
	}
}

func TestDeposit_NoOverflow128(t *testing.T) {
	// amount*deposit = 1e24 exceeds int64; result must be exact.
	a, r := alloc(t, MaxTotalMinor, MaxTotalMinor)
	if a[0] != MaxTotalMinor || r[0] != 0 {
		t.Fatal(a, r)
	}
	a, r = alloc(t, MaxTotalMinor/2, MaxTotalMinor/2, MaxTotalMinor/2)
	if a[0]+a[1] != MaxTotalMinor/2 || r[0]+r[1] != MaxTotalMinor-MaxTotalMinor/2 {
		t.Fatal(a, r)
	}
}

func TestDeposit_Property(t *testing.T) {
	run := func(seed int64) [][]int64 {
		rng := rand.New(rand.NewSource(seed))
		var all [][]int64
		for i := 0; i < 10000; i++ {
			n := 1 + rng.Intn(8)
			amts := make([]int64, n)
			var total int64
			for k := range amts {
				amts[k] = 1 + rng.Int63n(99999)
				total += amts[k]
			}
			dep := rng.Int63n(total + 1)
			a, r, err := AllocateDeposit(lines(amts...), money.FromMinor(dep))
			if err != nil {
				t.Fatal(err)
			}
			var sa, sr int64
			am, rm := minors(a), minors(r)
			for k := range amts {
				if am[k] < 0 || am[k] > amts[k] || am[k]+rm[k] != amts[k] {
					t.Fatalf("line %d: alloc %d rem %d amt %d", k, am[k], rm[k], amts[k])
				}
				sa += am[k]
				sr += rm[k]
				// math/big cross-check of the floor
				f := new(big.Int).Mul(big.NewInt(amts[k]), big.NewInt(dep))
				f.Div(f, big.NewInt(total))
				if am[k] < f.Int64() || am[k] > f.Int64()+1 {
					t.Fatalf("alloc %d outside floor %d (+1)", am[k], f.Int64())
				}
			}
			if sa != dep || sr != total-dep {
				t.Fatalf("sums %d %d for dep %d total %d", sa, sr, dep, total)
			}
			all = append(all, am)
		}
		return all
	}
	if !reflect.DeepEqual(run(42), run(42)) {
		t.Fatal("not deterministic")
	}
}

func TestDeposit_ApplyDeposit(t *testing.T) {
	cases := []struct {
		basket, dep int64
		want        Applied
	}{
		{7000, 2000, Applied{2000, 0, 5000}}, {1500, 2000, Applied{1500, 500, 0}}, {0, 2000, Applied{0, 2000, 0}},
		{-5, 100, Applied{0, 100, 0}},
	}
	for _, c := range cases {
		if got := ApplyDeposit(money.FromMinor(c.basket), money.FromMinor(c.dep)); got != c.want {
			t.Errorf("%d/%d: %+v want %+v", c.basket, c.dep, got, c.want)
		}
	}
}

func TestApplyDepositNegativeDepositIsNothingApplied(t *testing.T) {
	got := ApplyDeposit(money.FromMinor(1000), money.FromMinor(-500))
	want := Applied{Applied: 0, Excess: 0, Remainder: money.FromMinor(1000)}
	if got != want {
		t.Fatalf("ApplyDeposit(1000, -500) = %+v, want %+v", got, want)
	}
}
