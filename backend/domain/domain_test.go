package domain

import "testing"

func TestExactMoney(t *testing.T) {
	for _, tt := range []struct {
		s string
		n int64
	}{{"42.50", 4250}, {"-0.01", -1}, {"0", 0}, {"90071992547409.91", MaxSafeCents}} {
		n, e := ParseDollars(tt.s)
		if e != nil || n != tt.n {
			t.Fatalf("%s: %d %v", tt.s, n, e)
		}
	}
	for _, s := range []string{"1.001", "NaN", "1e6", "90071992547409.92"} {
		if _, e := ParseDollars(s); e == nil {
			t.Fatalf("accepted %s", s)
		}
	}
	if _, e := AddCents(MaxSafeCents, 1); e == nil {
		t.Fatal("overflow accepted")
	}
	if n, e := AddCents(-100, 99); e != nil || n != -1 {
		t.Fatal(n, e)
	}
}
