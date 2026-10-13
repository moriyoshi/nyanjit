package compile

import (
	"errors"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestSIMDComparisonQueriesAndReports(t *testing.T) {
	for _, arch := range []string{"amd64", "386", "arm64"} {
		for _, cond := range []ir.BranchCondition{ir.BranchEqual, ir.BranchLess} {
			b := builder.NewBuilder()
			a, c := must(b.ParamNative()), must(b.ParamNative())
			typ := ir.Vec(16, 8)
			mustDo(b.Return(must(b.MaskBits(must(b.VCompare(must(b.VLoad(typ, a)), cond, must(b.VLoad(typ, c))))))))
			p, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			for _, vectors := range []VectorPolicy{VectorsAuto(), VectorsOff()} {
				target := mustTarget(t, TargetSpec{Architecture: arch, Vectors: vectors})
				input, err := inputFromProgram(p, target.options(Func{Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}}))
				var report abi.EmissionReport
				if err == nil {
					report.VectorOperations = input.vectorOperations
				}
				if err != nil {
					t.Fatal(err)
				}
				info := target.Vectors()
				want := abi.Scalarized
				if cond == ir.BranchEqual && vectors == VectorsAuto() {
					want = abi.Native
				}
				if got := info.Lowering(ir.OpVCompare, typ, cond); got != want {
					t.Fatalf("%s query=%v want%v", arch, got, want)
				}
				if info.Lowering(ir.OpVCompare, typ) != abi.Scalarized {
					t.Fatal("condition-free query overstated support")
				}
				found := false
				for _, operation := range report.VectorOperations {
					if ir.Opcode(operation.Operation) == ir.OpVCompare {
						found = true
						if operation.Lowering != want {
							t.Fatalf("%s compare report=%v want%v", arch, operation.Lowering, want)
						}
					}
				}
				if !found {
					t.Fatal("missing comparison report")
				}
			}
		}
	}
	for _, spec := range []TargetSpec{{Architecture: "amd64", Vectors: VectorsRequire("avx2")}, {Architecture: "arm64", Vectors: VectorsRequire("sse2")}} {
		if _, err := NewTarget(spec); !errors.Is(err, ErrBackendLimit) {
			t.Fatalf("invalid features accepted: %v", err)
		}
		info := vectorInfo(uncheckedTarget(spec).options(Func{}))
		if info.PreferredBits != 0 || info.Lowering(ir.OpAdd, ir.Vec(4, 32)) != abi.Scalarized {
			t.Fatalf("invalid features accepted: %+v", info)
		}
	}
	for _, spec := range []TargetSpec{{Architecture: "386", Vectors: VectorsOff()}, {Architecture: "riscv64"}, {Architecture: "amd64", Vectors: VectorsOff()}} {
		if info := mustTarget(t, spec).Vectors(); info.PreferredBits != 0 {
			t.Fatalf("scalar target reports preferred vector width %d", info.PreferredBits)
		}
	}
}

func TestSIMDShiftQueriesAndReports(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "386", "riscv64", "ppc64le"} {
		for _, bits := range []uint8{8, 16, 32, 64} {
			typ := ir.Vec(128/uint16(bits), bits)
			for _, op := range []ir.Opcode{ir.OpShiftLeft, ir.OpShiftRight, ir.OpRotateLeft, ir.OpRotateRight} {
				for _, scalar := range []bool{false, true} {
					spec := TargetSpec{Architecture: arch}
					if scalar {
						spec.Vectors = VectorsOff()
					}
					target := mustTarget(t, spec)
					want := abi.Scalarized
					if !scalar && bits == 32 && (arch == "amd64" || arch == "386" || arch == "arm64") {
						want = abi.Native
						if op == ir.OpRotateLeft || op == ir.OpRotateRight {
							want = abi.Expanded
						}
					}
					info := target.Vectors()
					if got := info.Lowering(op, typ); got != want {
						t.Fatalf("%s width=%d op=%v scalar=%t: query %v want %v", arch, bits, op, scalar, got, want)
					}
					b := builder.NewBuilder()
					in, out := must(b.ParamNative()), must(b.ParamNative())
					x := must(b.VLoad(typ, in))
					var v builder.Value
					switch op {
					case ir.OpShiftLeft:
						v = must(b.ShiftLeft(x, 7))
					case ir.OpShiftRight:
						v = must(b.ShiftRight(x, 7))
					case ir.OpRotateLeft:
						v = must(b.RotateLeft(x, 7))
					case ir.OpRotateRight:
						v = must(b.RotateRight(x, 7))
					}
					mustDo(b.VStore(out, v))
					mustDo(b.Return(must(b.ConstNative(0))))
					p, err := b.Finish()
					if err != nil {
						t.Fatal(err)
					}
					input, err := inputFromProgram(p, target.options(Func{}))
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, operation := range input.vectorOperations {
						if ir.Opcode(operation.Operation) == op {
							found = true
							if operation.Lowering != want {
								t.Fatalf("%s width=%d op=%v scalar=%t: report %v want %v", arch, bits, op, scalar, operation.Lowering, want)
							}
						}
					}
					if !found {
						t.Fatalf("missing report for %s width=%d op=%v scalar=%t", arch, bits, op, scalar)
					}
				}
			}
		}
	}
}

func TestSIMDRotateScratchClobbers(t *testing.T) {
	for _, arch := range []string{"amd64", "386", "arm64"} {
		for _, op := range []ir.Opcode{ir.OpRotateLeft, ir.OpRotateRight} {
			for _, count := range []uint64{0, 1, 31, 32, 33, 64, 255} {
				for _, alias := range []bool{false, true} {
					a, b := backend.VectorRegister(3), backend.VectorRegister(4)
					if alias {
						b = a
					}
					ins := machine.Instruction{Op: machine.OpVector, Arithmetic: op, Bits: 32, A: a, B: b, Imm: count}
					clobbers, err := fpClobbersFromPhysical(physicalProgram{insns: []locatedInsn{{ins: ins}}}, nil, newMachineTarget(compileOptions{Architecture: arch}))
					if err != nil {
						t.Fatal(err)
					}
					scratch := "XMM0"
					want := count%32 != 0
					if arch == "arm64" {
						scratch = "V30"
						want = want && alias
					}
					if got := slices.Contains(clobbers, scratch); got != want {
						t.Fatalf("%s op=%v count=%d alias=%t: clobbers=%v, want scratch=%t", arch, op, count, alias, clobbers, want)
					}
				}
			}
		}
	}
}

func Test386SIMDPhysicalTemps(t *testing.T) {
	f := backend.VectorRegister
	for _, tc := range []struct {
		i    machine.Instruction
		want []string
	}{
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVSplat, Bits: 64, A: f(3), B: 0, HighB: 2}, []string{"XMM3", "XMM2"}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVExtract, Bits: 64, A: 0, HighA: 2, B: f(3)}, []string{"XMM0", "XMM2"}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVExtract, Bits: 32, A: 0, B: f(3)}, []string{"XMM0"}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVInsertZero, Bits: 64, A: f(3), B: f(4), C: 0, HighC: 2}, []string{"XMM3", "XMM0", "XMM2"}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpSub, Bits: 32, A: f(4), B: f(3), C: f(4)}, []string{"XMM4", "XMM1"}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpAdd, Bits: 32, A: f(3), B: f(3), C: f(4)}, []string{"XMM3"}},
		{machine.Instruction{Op: machine.OpVectorSpill, A: f(3)}, nil},
		{machine.Instruction{Op: machine.OpVectorReload, A: f(3)}, []string{"XMM3"}},
	} {
		got, err := fpClobbersFromPhysical(physicalProgram{insns: []locatedInsn{{ins: tc.i}}}, nil, newMachineTarget(compileOptions{Architecture: "386"}))
		if err != nil || !slices.Equal(got, tc.want) {
			t.Fatalf("%+v got%v,%v want%v", tc.i, got, err, tc.want)
		}
	}
}
