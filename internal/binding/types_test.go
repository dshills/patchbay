package binding

import "testing"

func TestPrecedenceAndOverlap(t *testing.T) {
	press := &Target{Action: "test"}
	tiers := []Binding{
		{Control: "key", Press: press},
		{Device: "deck", Control: "key", Press: press},
		{Control: "key", When: map[string]string{"mode": "dev"}, Press: press},
		{Device: "deck", Control: "key", When: map[string]string{"mode": "dev"}, Press: press},
	}
	for i, a := range tiers {
		if a.Rank() != i {
			t.Fatalf("rank %d: %d", i, a.Rank())
		}
		for j, b := range tiers {
			if Overlaps(a, b) != (i == j) {
				t.Fatalf("tier overlap %d/%d", i, j)
			}
		}
	}
	a := Binding{Control: "key", When: map[string]string{"mode": "dev"}, Press: press}
	if !Overlaps(a, Binding{Control: "key", When: map[string]string{"values.role": "coder"}, Press: press}) {
		t.Fatal("overlapping predicates accepted")
	}
	for _, b := range []Binding{
		{Control: "key", When: map[string]string{"mode": "meeting"}, Press: press},
		{Control: "other", When: a.When, Press: press},
		{Control: "key", When: a.When, Release: press},
		{Device: "other", Control: "key", When: a.When, Press: press},
	} {
		if Overlaps(a, b) {
			t.Fatalf("false ambiguity: %+v", b)
		}
	}
}
