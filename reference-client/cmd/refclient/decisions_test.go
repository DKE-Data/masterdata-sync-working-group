package main

import "testing"

// TestAlike pins what reaches a person and what does not.
//
// Both directions matter and for different reasons: a pair that is not alike is
// a question nobody is asked, and a pair that is alike is a load stopped until
// somebody answers. Either one is wrong in a way the screens do not make
// obvious, so the rule is stated here rather than left to be discovered.
func TestAlike(t *testing.T) {
	cases := []struct {
		delivered, held string
		want            bool
		why             string
	}{
		{"Hof Nord", "Hof Nord GmbH", true, "a legal form on one side is the case a person is for"},
		{"Hof Nord GmbH", "Hof Nord", true, "and the same the other way round"},
		{"hof  nord", "Hof Nord", true, "case and spacing are not a difference"},
		{"Probe Farm", "Testhof Süd", false, "unrelated names are not worth asking about"},
		{"Nordacker", "", false, "a record with no name matches nothing"},
		{"", "Nordacker", false, "and neither does an object with none"},
		{"Hof", "Hofgut Süd", false, "short names would otherwise match everything"},
		{"Hof", "hof", true, "but a short name still matches itself"},
	}

	for _, c := range cases {
		if got := alike(c.delivered, c.held); got != c.want {
			t.Errorf("alike(%q, %q) = %v, want %v: %s",
				c.delivered, c.held, got, c.want, c.why)
		}
	}
}
