package ir_test

import (
	"github.com/moriyoshi/nyanjit/ir"
	"testing"
)

func TestSIMDLogicalTypes(t *testing.T) {
	if ir.Vec(16, 32).Bits() != 512 || ir.Mask(16).Bits() != 16 || ir.Scalar(128) == ir.Vec(2, 64) {
		t.Fatal("shape identity or total width is incorrect")
	}
	for _, typ := range []ir.Type{{}, ir.Vec(0, 8), ir.Vec(1, 64), ir.Vec(128, 8), ir.Vec(16, 128), ir.Mask(65), ir.Scalar(1)} {
		if typ.Valid() {
			t.Fatalf("invalid shape accepted: %+v", typ)
		}
	}
}
