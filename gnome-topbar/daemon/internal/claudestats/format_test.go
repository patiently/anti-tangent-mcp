package claudestats

import "testing"

func TestHumanTokens(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{512, "512"},
		{4821, "4.8k"},
		{512004, "512k"},
		{4821093, "4.8M"},
		{33106912, "33.1M"},
	}
	for _, c := range cases {
		if got := HumanTokens(c.n); got != c.want {
			t.Errorf("HumanTokens(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestUsageCostTokens(t *testing.T) {
	u := &Usage{CostUSD: 12.47, TotalTokens: 4821093}
	if got := u.CostTokens(); got != "$12.47 · 4.8M tok" {
		t.Errorf("CostTokens() = %q, want %q", got, "$12.47 · 4.8M tok")
	}
	if got := (*Usage)(nil).CostTokens(); got != "" {
		t.Errorf("nil CostTokens() = %q, want empty", got)
	}
}

func TestWindowUtilLabel(t *testing.T) {
	p := func(f float64) *float64 { return &f }
	cases := []struct {
		w    *Window
		want string
	}{
		{nil, ""},
		{&Window{}, ""},
		{&Window{Utilization: p(26)}, "26%"},
		{&Window{Utilization: p(80)}, "80% ⚠"},
		{&Window{Utilization: p(91)}, "91% ⚠"},
	}
	for _, c := range cases {
		if got := c.w.UtilLabel(); got != c.want {
			t.Errorf("UtilLabel(%+v) = %q, want %q", c.w, got, c.want)
		}
	}
}
