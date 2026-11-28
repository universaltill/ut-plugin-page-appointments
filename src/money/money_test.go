package money

import (
	"encoding/json"
	"testing"
)

func TestMoney_JSON(t *testing.T) {
	b, err := json.Marshal(FromMinor(1234))
	if err != nil || string(b) != "1234" {
		t.Fatalf("marshal = %s, %v; want 1234", b, err)
	}
	var m Money
	if err := json.Unmarshal([]byte("1234"), &m); err != nil || m != 1234 {
		t.Fatalf("unmarshal = %v, %v", m, err)
	}
	if got := m.String(); got != "12.34" {
		t.Fatalf("String = %q, want 12.34", got)
	}
}

func TestMoney_Arithmetic(t *testing.T) {
	a, b := FromMinor(500), FromMinor(250)
	if a.Add(b) != 750 || a.Sub(b) != 250 || b.Neg() != -250 || b.Scale(3) != 750 {
		t.Fatal("arithmetic wrong")
	}
	if !Zero.IsZero() || !a.IsPositive() || !b.Neg().IsNegative() || a.IsNegative() || a.IsZero() || Zero.IsPositive() {
		t.Fatal("predicates wrong")
	}
	if a.Minor() != 500 {
		t.Fatal("Minor")
	}
	if FromMinor(-5).String() != "-0.05" {
		t.Fatalf("negative format: %s", FromMinor(-5).String())
	}
}

func TestMoney_ScanValue(t *testing.T) {
	v, _ := FromMinor(9).Value()
	if v.(int64) != 9 {
		t.Fatal("Value")
	}
	cases := []struct {
		in   any
		want Money
		err  bool
	}{
		{nil, 0, false}, {int64(5), 5, false}, {7, 7, false}, {[]byte("12"), 12, false},
		{"13", 13, false}, {[]byte("x"), 0, true}, {"y", 0, true}, {1.5, 0, true},
	}
	for _, c := range cases {
		var m Money
		err := m.Scan(c.in)
		if (err != nil) != c.err || (!c.err && m != c.want) {
			t.Errorf("Scan(%v) = %v, %v", c.in, m, err)
		}
	}
}
