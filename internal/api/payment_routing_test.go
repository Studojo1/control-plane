package api

import "testing"

func TestUsesRazorpay(t *testing.T) {
	cases := []struct {
		country   string
		dodoReady bool
		want      bool
	}{
		{"IN", true, true},
		{"UNKNOWN", true, true}, // lookup failed: never guess international
		{"", true, true},
		{"US", true, false}, // the only Dodo case
		{"GB", true, false},
		{"US", false, true}, // Dodo not configured: stay on Razorpay
		{"UNKNOWN", false, true},
	}
	for _, c := range cases {
		if got := usesRazorpay(c.country, c.dodoReady); got != c.want {
			t.Errorf("usesRazorpay(%q, %v) = %v, want %v", c.country, c.dodoReady, got, c.want)
		}
	}
}

func TestDodoNotReadyWithoutExplicitTestMode(t *testing.T) {
	t.Setenv("DODO_TEST_MODE", "")
	h := &Handler{DodoProductCareer: "prod_x"}
	if h.dodoReady() {
		t.Fatal("no client and no explicit test mode must not be ready")
	}
}
