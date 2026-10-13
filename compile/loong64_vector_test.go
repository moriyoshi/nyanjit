package compile

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestLoong64LSXFeatures(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		cpu                  abi.CPUProfile
		vectors              VectorPolicy
		enabled, unsupported bool
	}{
		{name: "baseline"},
		{name: "profile-lsx", cpu: abi.CPUProfile{Extensions: []string{"lsx"}}, enabled: true},
		{name: "profile-lasx", cpu: abi.CPUProfile{Extensions: []string{"lasx"}}, enabled: true},
		{name: "explicit-lsx", vectors: VectorsRequire("lsx"), enabled: true},
		{name: "explicit-lasx", vectors: VectorsRequire("lasx"), enabled: true},
		{name: "disabled", cpu: abi.CPUProfile{Extensions: []string{"lsx"}}, vectors: VectorsOff()},
		{name: "baseline-rejects-lsx", cpu: abi.CPUProfile{Level: "la64v1.0"}, vectors: VectorsRequire("lsx"), unsupported: true},
		{name: "lsx-rejects-lasx", cpu: abi.CPUProfile{Extensions: []string{"lsx"}}, vectors: VectorsRequire("lasx"), unsupported: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, err := NewTarget(TargetSpec{Architecture: "loong64", CPU: tc.cpu, Vectors: tc.vectors})
			if tc.unsupported {
				if !errors.Is(err, ErrCPUUnsupported) {
					t.Fatalf("got %v want CPU unsupported", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			info := target.Vectors()
			if (info.PreferredBits == 128) != tc.enabled {
				t.Fatalf("wrong preferred width: %+v", info)
			}
			if tc.enabled && !slices.Contains(info.Features, "lsx") {
				t.Fatal("missing implied LSX")
			}
			for _, bits := range []uint8{8, 16, 32, 64} {
				typ := ir.Vec(128/uint16(bits), bits)
				for _, operation := range []ir.Opcode{ir.OpVLoad, ir.OpVStore, ir.OpVSplat, ir.OpVConst, ir.OpVExtract, ir.OpVInsert, ir.OpVInsertZero, ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpMul, ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight, ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy, ir.OpVCarrylessMul} {
					want := abi.Scalarized
					if tc.enabled && operation != ir.OpVCarrylessMul {
						want = abi.Native
						if _, by := op.ShiftByOperation(operation); by {
							want = abi.Expanded
						}
					}
					if got := info.Lowering(operation, typ); got != want {
						t.Fatalf("op%d bits%d got%v want%v", operation, bits, got, want)
					}
					wide := ir.Vec(256/uint16(bits), bits)
					wideWant := abi.Scalarized
					if want != abi.Scalarized {
						wideWant = abi.Split
					}
					if got := info.Lowering(operation, wide); got != wideWant {
						t.Fatalf("wide op%d bits%d got%v want%v", operation, bits, got, wideWant)
					}
				}
			}
			want := abi.Scalarized
			if tc.enabled {
				want = abi.Expanded
			}
			if info.Lowering(ir.OpVMaskBits, ir.Mask(16)) != want {
				t.Fatal("wrong mask extraction lowering")
			}
		})
	}
}

func TestLoong64LSXAssemblyAndReports(t *testing.T) {
	b := builder.NewBuilder()
	in, out, count := must(b.ParamNative()), must(b.ParamNative()), must(b.Param())
	x := must(b.VLoad(ir.Vec(4, 32), in))
	x = must(b.RotateLeft(must(b.ShiftRightArithmeticBy(x, count)), 7))
	mustDo(b.VStore(out, x))
	mustDo(b.Return(must(b.ConstNative(0))))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	spec := TargetSpec{Architecture: "loong64", Vectors: VectorsRequire("lsx")}
	f := Func{Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}}
	text, err := lowerListing(p, spec, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"vld ", "vst ", "vsra.w ", "vrotri.w ", "vandn.v "} {
		if !strings.Contains(text, s) {
			t.Fatalf("missing %s:\n%s", s, text)
		}
	}
	input, err := inputFromProgram(p, mustTarget(t, spec).options(f))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range input.vectorOperations {
		if ir.Opcode(v.Operation) == ir.OpShiftRightArithmeticBy {
			found = true
			if v.Lowering != abi.Expanded {
				t.Fatalf("wrong report: %+v", v)
			}
		}
	}
	if !found {
		t.Fatal("missing variable shift report")
	}
	clobbers, err := fpClobbersFromPhysical(physicalProgram{insns: []locatedInsn{{ins: machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpShiftRightArithmeticBy, Bits: 32, A: backend.VectorRegister(8), B: backend.VectorRegister(9), C: 4}}}}, nil, newMachineTarget(compileOptions{Architecture: "loong64"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"F0", "F1", "F2", "F8"} {
		if !slices.Contains(clobbers, name) {
			t.Fatalf("missing %s in %v", name, clobbers)
		}
	}
}

func TestLoong64LSXMemorySelection(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		b := builder.NewBuilder()
		in, out, index := must(b.ParamBits(64)), must(b.ParamBits(64)), must(b.ParamBits(64))
		offset := index
		if !indexed {
			offset = must(b.Const(2047))
		}
		x := must(b.VLoad(ir.Vec(4, 32), must(b.Add(in, offset))))
		mustDo(b.VStore(must(b.Add(out, offset)), x))
		mustDo(b.Return(must(b.Const(0))))
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		text, err := lowerListing(p, TargetSpec{Architecture: "loong64", Vectors: VectorsRequire("lsx")}, Func{})
		if err != nil {
			t.Fatal(err)
		}
		if indexed {
			if !strings.Contains(text, "vldx ") || !strings.Contains(text, "vstx ") {
				t.Fatalf("unfolded indexed address:\n%s", text)
			}
		} else if !strings.Contains(text, "2047(r") {
			t.Fatalf("unfolded immediate address:\n%s", text)
		}
	}
}
