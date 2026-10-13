package compile

import (
	"fmt"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

func Test386FloatCopiesKeepABIWords(t *testing.T) {
	p := machineProgram{Result: 1, Values: []machineValue{{Bits: 64, Words: 2}, {Bits: 64, Words: 2}}, Instructions: []machineInstruction{{Op: mFloatUnary, Arithmetic: ir.OpFSqrt, Bits: 64, Dst: 1, A: 0}}}
	q := assignFloatResidency(p, 32)
	for _, i := range q.Instructions {
		if i.Op == machine.SelectedFloatMove && (q.Values[i.Dst].Words != 2 || q.Values[i.A].Words != 2) {
			t.Fatalf("copy lost ABI word count: %+v", i)
		}
	}
	if q.Values[1].Class != backend.Float || q.Values[1].Words != 2 {
		t.Fatal("FP residency changed ABI width")
	}
}

func Test386FPVarLoopStaysInXMM(t *testing.T) {
	for _, w := range []uint8{32, 64} {
		for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
			t.Run(fmt.Sprintf("width%d/stack%d", w, stack), func(t *testing.T) {
				b := builder.NewBuilder()
				x := must(b.ParamBits(w))
				v := must(b.Var(x))
				k := must(b.Var(must(b.ConstBits(32, 3))))
				loop, done := b.NewLabel(), b.NewLabel()
				mustDo(b.Jump(loop))
				mustDo(b.Bind(loop))
				mustDo(b.Branch(must(b.IsZero(must(b.Get(k)))), done))
				mustDo(b.Set(v, must(b.FAdd(must(b.Get(v)), must(b.Get(v))))))
				mustDo(b.Set(k, must(b.Sub(must(b.Get(k)), must(b.ConstBits(32, 1))))))
				mustDo(b.Jump(loop))
				mustDo(b.Bind(done))
				mustDo(b.Return(must(b.Get(v))))
				p := codegenFinish(t, b)
				spec := TargetSpec{Architecture: "386"}
				f := Func{Stack: StackPolicy{Practice: stack}}
				if stack == abi.StackPlatform {
					f.Stack.BytesAvailable = 8192
				}
				plan := planVariableHomes(p, newMachineTarget(mustTarget(t, spec).options(f)), stack)
				h := testVariableHome(t, plan, 0, 0)
				if !plan.FloatHomes[0] || h.Register < 0 || !backend.IsVectorRegister(physReg(h.Register)) || plan.Words[0] != int(w/32) {
					t.Fatalf("wrong FP home: %+v", plan)
				}
				text, e := lowerListing(p, spec, f)
				if e != nil {
					t.Fatal(e)
				}
				start, end := strings.Index(text, "cmp "), strings.LastIndex(text, "\njmp ")
				if start < 0 || end <= start {
					t.Fatalf("missing loop: %s", text)
				}
				for _, row := range strings.Split(text[start:end], "\n") {
					if strings.Contains(row, "movd ") {
						t.Fatalf("GP round-trip in FP loop: %s\n%s", row, text)
					}
				}
			})
		}
	}
}

func Test386FloatSpillFootprints(t *testing.T) {
	for _, w := range []uint8{32, 64} {
		i := machine.Instruction{Op: machine.OpFloatSpill, A: backend.VectorRegister(3), Bits: w, Slot: 7}
		if got := referencedSpillSlots([]machine.Instruction{i}, 32); got != 7+int(w/32) {
			t.Fatalf("width%d footprint=%d", w, got)
		}
	}
}

// Count concrete statements, including each frame operation in the 386 listing.
func Test386FloatChainInstructionBudget(t *testing.T) {
	b := builder.NewBuilder()
	x := must(b.Param())
	sum := x
	for i := 0; i < 10; i++ {
		sum = must(b.FAdd(sum, x))
	}
	mustDo(b.Return(sum))
	text, e := lowerListing(codegenFinish(t, b), TargetSpec{Architecture: "386"}, Func{Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}})
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, row := range strings.Split(text, "\n") {
		if strings.TrimSpace(row) != "" {
			count += 1 + strings.Count(row, ";")
		}
	}
	if count > 31 {
		t.Fatalf("FP chain has %d instructions (P2: 114; P3 budget: 31):\n%s", count, text)
	}
}

func Test386UnusedOuterFPHomeOnlySpillsUnderPressure(t *testing.T) {
	for _, w := range []uint8{32, 64} {
		for _, pressure := range []bool{false, true} {
			b := builder.NewBuilder()
			constant := func(x float64) builder.Value {
				if w == 32 {
					return must(b.Float32(float32(x)))
				}
				return must(b.Float64(x))
			}
			outer := must(b.Var(constant(1.5)))
			mustDo(b.Enter())
			count := 1
			if pressure {
				count = 8
			}
			locals := make([]builder.Var, count)
			for i := range locals {
				locals[i] = must(b.Var(constant(float64(i + 1))))
			}
			for _, v := range locals {
				mustDo(b.Set(v, must(b.FAdd(must(b.Get(v)), constant(1)))))
			}
			mustDo(b.Leave())
			mustDo(b.Return(must(b.FAdd(must(b.Get(outer)), constant(1)))))
			p := codegenFinish(t, b)
			o := mustTarget(t, TargetSpec{Architecture: "386"}).options(Func{Stack: StackPolicy{Practice: abi.StackNoUse}})
			target := newMachineTarget(o)
			input, e := inputFromProgram(p, o)
			if e != nil {
				t.Fatal(e)
			}
			plan := input.homes
			root, inner := testVariableHome(t, plan, 0, 0), testVariableHome(t, plan, 1, 0)
			if root.Register < 0 || !backend.IsVectorRegister(physReg(root.Register)) {
				t.Fatalf("width%d outer has no FP home", w)
			}
			if pressure && inner.Register >= 0 || !pressure && inner.Register != root.Register {
				t.Fatalf("width%d pressure%t home changed incorrectly: %+v %+v", w, pressure, root, inner)
			}
			selected, e := selectProgramResultParts(input.code, input.leaves, input.arity, input.result, input.resultParts, target)
			if e != nil {
				t.Fatal(e)
			}
			bindVariableHomes(&selected, input.source, 0, plan)
			physical, _, _, e := allocateAndEmit(selected, target, newMachineEmitter(o))
			if e != nil {
				t.Fatal(e)
			}
			stores, loads := 0, 0
			for _, at := range physical.insns {
				if at.ins.Slot == root.Slot && (at.ins.Op == machine.OpFloatSpill || at.ins.Op == machine.OpFloatReload) {
					if at.ins.Bits != w {
						t.Fatalf("width%d home accessed as%d", w, at.ins.Bits)
					}
					if at.ins.Op == machine.OpFloatSpill {
						stores++
					} else {
						loads++
					}
				}
			}
			if pressure && (stores != 1 || loads != 1) || !pressure && (stores != 0 || loads != 0) {
				t.Fatalf("width%d pressure%t outer stores/loads=%d/%d", w, pressure, stores, loads)
			}
		}
	}
}

func Test386FloatLiteralInstructionBudget(t *testing.T) {
	b := builder.NewBuilder()
	x := must(b.Param())
	c := must(b.Float64(1.25))
	mustDo(b.Return(must(b.FAdd(must(b.FMul(x, c)), c))))
	text, e := lowerListing(codegenFinish(t, b), TargetSpec{Architecture: "386"}, Func{})
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	loads := 0
	for _, row := range strings.Split(text, "\n") {
		row = strings.TrimSpace(row)
		if row != "" && !strings.HasPrefix(row, ".") {
			count += 1 + strings.Count(row, ";")
		}
		if strings.Contains(row, "[literal +") {
			loads++
		}
	}
	if count > 13 || loads != 1 {
		t.Fatalf("literal chain count=%d loads=%d, want at most13 and one shared load:\n%s", count, loads, text)
	}
}
