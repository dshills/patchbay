// Package identity creates opaque identifiers without shared mutable counters.
package identity

import "crypto/rand"

func New() string { return rand.Text() }
