package permission

import "testing"

func TestSafetyFloor(t *testing.T) {
	ordered := []Permission{Safe, Confirm, Dangerous}
	for i, a := range ordered {
		for j, b := range ordered {
			if !a.Valid() || Strongest(a, b) != ordered[max(i, j)] {
				t.Fatal("invalid safety order")
			}
		}
	}
	if Permission("invalid").Valid() || Strongest("invalid", Safe) != Dangerous {
		t.Fatal("invalid classification must fail closed")
	}
}
