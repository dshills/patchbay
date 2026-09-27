package parameter

import (
	"errors"
	"math"
	"math/big"
)

// Rotate computes against normalized metadata without mutating the definition.
// Wide intermediates avoid overflow before bounds are applied.
func (d Definition) Rotate(delta int64) (any, error) {
	switch d.Type {
	case Integer:
		lo, hi := int64(math.MinInt64), int64(math.MaxInt64)
		if d.Min != nil {
			lo = d.Min.(int64)
		}
		if d.Max != nil {
			hi = d.Max.(int64)
		}
		n := new(big.Int).Mul(big.NewInt(delta), big.NewInt(d.Step.(int64)))
		n.Add(n, big.NewInt(d.Value.(int64)))
		if n.Cmp(big.NewInt(lo)) < 0 {
			return lo, nil
		}
		if n.Cmp(big.NewInt(hi)) > 0 {
			return hi, nil
		}
		return n.Int64(), nil
	case Float:
		n := new(big.Rat).SetFloat64(d.Step.(float64))
		n.Mul(n, new(big.Rat).SetInt64(delta))
		n.Add(n, new(big.Rat).SetFloat64(d.Value.(float64)))
		lo, hi := -math.MaxFloat64, math.MaxFloat64
		if d.Min != nil {
			lo = d.Min.(float64)
		}
		if d.Max != nil {
			hi = d.Max.(float64)
		}
		if n.Cmp(new(big.Rat).SetFloat64(lo)) < 0 {
			return lo, nil
		}
		if n.Cmp(new(big.Rat).SetFloat64(hi)) > 0 {
			return hi, nil
		}
		value, _ := n.Float64()
		return value, nil
	default:
		return nil, errors.New("rotation requires a numeric parameter")
	}
}
