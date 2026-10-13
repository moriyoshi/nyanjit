package builder

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestScalarAPIsRejectVectorsAndMasks(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		for _, mask := range []bool{false, true} {
			b := NewBuilder()
			v := mustVector(t, b, ir.Vec(2, 64))
			if !deferred || mask {
				var err error
				v, err = b.materialize(v)
				if err != nil {
					t.Fatal(err)
				}
			}
			if mask {
				m, err := b.VCompare(v, ir.BranchEqual, v)
				if err != nil {
					t.Fatal(err)
				}
				v = m.Value()
			}
			c := mustScalar(t, b, 64)
			tests := map[string]func() error{
				"Compare":          func() error { _, e := b.Compare(v, ir.BranchEqual, v); return e },
				"CompareImmediate": func() error { _, e := b.CompareImmediate(v, ir.BranchEqual, 0); return e },
				"IsZero":           func() error { _, e := b.IsZero(v); return e },
				"IsNegative":       func() error { _, e := b.IsNegative(v); return e },
				"Concat":           func() error { _, e := b.Concat(v, v); return e },
				"CastUnsigned":     func() error { _, e := b.CastUnsigned(v, 64); return e },
				"ExtractBits":      func() error { _, e := b.ExtractBits(v, 0, 32); return e },
				"Select result":    func() error { _, e := b.Select(c, v, v); return e },
				"Select condition": func() error { _, e := b.Select(v, c, c); return e },
				"Load64":           func() error { _, e := b.Load64(v); return e },
				"LoadBits":         func() error { _, e := b.LoadBits(v, 32); return e },
				"Store64":          func() error { return b.Store64(c, v) },
				"Store8":           func() error { return b.Store8(c, v) },
				"Store32":          func() error { return b.Store32(c, v) },
				"StoreBits":        func() error { return b.StoreBits(c, v) },
				"FAdd mixed":       func() error { _, e := b.FAdd(c, v); return e },
				"FMA mixed":        func() error { _, e := b.FMA(c, c, v); return e },
				"SignExtend":       func() error { _, e := b.SignExtend(v, 32); return e },
				"CallGo":           func() error { _, e := b.CallGo(c, v); return e },
			}
			for name, f := range tests {
				if err := f(); !errors.Is(err, ErrInvalidFunction) {
					t.Errorf("%s deferred=%v mask=%v: %v", name, deferred, mask, err)
				}
			}
			if v.CastUnsigned(64).Type().Valid() || v.Select(c, c).Type().Valid() || c.Select(v, v).Type().Valid() || v.SignExtend(32).Type().Valid() {
				t.Error("scalar expression accepted vector/mask")
			}
		}
	}
}
