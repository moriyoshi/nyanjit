package compile

import (
	"errors"
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"testing"
)

func TestSIMDComparisonQueriesAndReports(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		for _, cond := range []nyanjit.BranchCondition{nyanjit.BranchEqual, nyanjit.BranchLess} {
			b := nyanjit.NewBuilder()
			a, c := b.ParamNative(), b.ParamNative()
			typ := nyanjit.Vec(16, 8)
			b.Return(b.MaskBits(b.VCompare(b.VLoad(typ, a), cond, b.VLoad(typ, c))))
			p, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			for _, features := range [][]string{nil, {}} {
				opts := abi.CompileOptions{Architecture: arch, Features: features, Stack: abi.StackPlatform, StackBytesAvailable: 65536}
				input, err := inputFromProgram(p, opts)
				var report abi.EmissionReport
				if err == nil {
					report.VectorOperations = input.vectorOperations
				}
				if err != nil {
					t.Fatal(err)
				}
				info := Vectors(opts)
				if info.Err != nil {
					t.Fatal(info.Err)
				}
				want := Scalarized
				if cond == nyanjit.BranchEqual && features == nil {
					want = Native
				}
				if got := info.Lowering(ir.OpVCompare, typ, cond); got != want {
					t.Fatalf("%s query=%v want%v", arch, got, want)
				}
				if info.Lowering(ir.OpVCompare, typ) != Scalarized {
					t.Fatal("condition-free query overstated support")
				}
				found := false
				for _, op := range report.VectorOperations {
					if ir.Opcode(op.Operation) == ir.OpVCompare {
						found = true
						if op.Lowering != want {
							t.Fatalf("%s compare report=%v want%v", arch, op.Lowering, want)
						}
					}
				}
				if !found {
					t.Fatal("missing comparison report")
				}
			}
		}
	}
	for _, opts := range []abi.CompileOptions{{Architecture: "amd64", Features: []string{"avx2"}}, {Architecture: "arm64", Features: []string{"sse2"}}} {
		info := Vectors(opts)
		if !errors.Is(info.Err, ErrBackendLimit) || info.PreferredBits != 0 || info.Lowering(ir.OpAdd, ir.Vec(4, 32)) != Scalarized {
			t.Fatalf("invalid features accepted: %+v", info)
		}
	}
	for _, opts := range []abi.CompileOptions{{Architecture: "386"}, {Architecture: "riscv64"}, {Architecture: "amd64", Features: []string{}}} {
		if info := Vectors(opts); info.PreferredBits != 0 {
			t.Fatalf("scalar target reports preferred vector width %d", info.PreferredBits)
		}
	}
}
