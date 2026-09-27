// Package permission defines and enforces action safety classifications.
package permission

type Permission string

const (
	Safe      Permission = "safe"
	Confirm   Permission = "confirm"
	Dangerous Permission = "dangerous"
)

func (p Permission) Valid() bool {
	return p == Safe || p == Confirm || p == Dangerous
}

func Strongest(a, b Permission) Permission {
	if !a.Valid() || !b.Valid() {
		return Dangerous
	}
	if a == Dangerous || b == Dangerous {
		return Dangerous
	}
	if a == Confirm || b == Confirm {
		return Confirm
	}
	return Safe
}
