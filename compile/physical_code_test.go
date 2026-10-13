package compile

import (
	"math"
	"math/rand"
	"reflect"
	"slices"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/backend/machine"
	targetamd64 "github.com/moriyoshi/nyanjit/backend/target/amd64"
	targetarm64 "github.com/moriyoshi/nyanjit/backend/target/arm64"
	targeti386 "github.com/moriyoshi/nyanjit/backend/target/i386"
	targetloong64 "github.com/moriyoshi/nyanjit/backend/target/loong64"
	targetppc64 "github.com/moriyoshi/nyanjit/backend/target/ppc64"
	targetriscv64 "github.com/moriyoshi/nyanjit/backend/target/riscv64"
)

// decoded returns a copy of c's instructions with their PCs.
func (c *physicalCode) decoded() []locatedInsn {
	insns := make([]locatedInsn, c.count())
	for n := range insns {
		insns[n] = locatedInsn{ins: c.at(n), pc: c.pcAt(n)}
	}
	return insns
}

// codeOf packs insns.
func codeOf(insns ...locatedInsn) physicalCode {
	var c physicalCode
	for n := range insns {
		c.add(&insns[n].ins, insns[n].pc)
	}
	return c
}

// instructionFields lists machine.Instruction's fields. packedInsn, pack and
// physicalCode.decode carry each of them; a new field needs all three.
var instructionFields = []string{
	"Data", "Symbol", "Table", "Imm", "Disp", "CallAreaBytes", "Slot", "VectorType", "Op", "Bits",
	"FlagRequest", "PreserveFlags", "Arithmetic", "Condition", "Comparison", "ByteOrder", "Barrier",
	"A", "B", "C", "D", "E", "HighA", "HighB", "HighC", "CarryMode", "Indexed", "Long", "Far",
}

func TestPackedInsnCoversInstruction(t *testing.T) {
	typ := reflect.TypeFor[machine.Instruction]()
	var got []string
	for i := range typ.NumField() {
		got = append(got, typ.Field(i).Name)
	}
	if !slices.Equal(got, instructionFields) {
		t.Fatalf("machine.Instruction fields = %v; update packedInsn, pack, physicalCode.decode and instructionFields", got)
	}
}

func TestPackedInsnIsCompactAndPointerFree(t *testing.T) {
	// 64 bytes on 64-bit hosts and 52 on 32-bit ones, against 136 and 88
	// for a locatedInsn.
	want := uintptr(64)
	if unsafe.Sizeof(int(0)) == 4 {
		want = 52
	}
	if size := unsafe.Sizeof(packedInsn{}); size != want {
		t.Fatalf("packedInsn is %d bytes; want %d", size, want)
	}
	var walk func(reflect.Type)
	walk = func(typ reflect.Type) {
		switch typ.Kind() {
		case reflect.Struct:
			for i := range typ.NumField() {
				walk(typ.Field(i).Type)
			}
		case reflect.Array:
			walk(typ.Elem())
		case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		default:
			t.Fatalf("packedInsn holds a %v", typ)
		}
	}
	walk(reflect.TypeFor[packedInsn]())
}

// sameInstruction reports whether a and b are equal field for field,
// telling nil from empty slices and keeping the same backing arrays.
func sameInstruction(a, b machine.Instruction) bool {
	return reflect.DeepEqual(a, b) &&
		unsafe.SliceData(a.Data) == unsafe.SliceData(b.Data) && cap(a.Data) == cap(b.Data) &&
		unsafe.SliceData(a.Table) == unsafe.SliceData(b.Table) && cap(a.Table) == cap(b.Table)
}

// randomInstruction sets every field of an instruction from r, each field
// zero about a third of the time.
func randomInstruction(r *rand.Rand) machine.Instruction {
	var ins machine.Instruction
	var fill func(reflect.Value)
	fill = func(v reflect.Value) {
		if v.Kind() != reflect.Struct && r.Intn(3) == 0 {
			return
		}
		switch v.Kind() {
		case reflect.Struct:
			for i := range v.NumField() {
				fill(v.Field(i))
			}
		case reflect.Bool:
			v.SetBool(true)
		case reflect.Int:
			v.SetInt([]int64{1, -1, math.MaxInt, math.MinInt, int64(r.Int())}[r.Intn(5)])
		case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			v.SetUint(r.Uint64())
		case reflect.String:
			v.SetString([]string{"sym", "runtime·gateway"}[r.Intn(2)])
		case reflect.Slice:
			length := r.Intn(3)
			s := reflect.MakeSlice(v.Type(), length, length+r.Intn(2))
			for i := range length {
				s.Index(i).Set(reflect.ValueOf(r.Uint64()).Convert(v.Type().Elem()))
			}
			v.Set(s)
		default:
			panic("unhandled field kind " + v.Kind().String())
		}
	}
	fill(reflect.ValueOf(&ins).Elem())
	return ins
}

// Packing keeps every opcode and every field, alone and together, with
// any PC.
func TestPhysicalCodeRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	var want []locatedInsn
	pcs := []int{0, 1, math.MaxInt32, -1, math.MaxInt, math.MinInt}
	for op := range 256 {
		want = append(want, locatedInsn{ins: machine.Instruction{Op: machine.Opcode(op)}, pc: pcs[op%len(pcs)]})
		ins := randomInstruction(r)
		ins.Op = machine.Opcode(op)
		want = append(want, locatedInsn{ins: ins, pc: r.Int()})
	}
	typ := reflect.TypeFor[machine.Instruction]()
	for i := range typ.NumField() {
		for _, value := range fieldValues(typ.Field(i).Type) {
			var ins machine.Instruction
			reflect.ValueOf(&ins).Elem().Field(i).Set(value)
			want = append(want, locatedInsn{ins: ins, pc: len(want)})
		}
	}
	code := codeOf(want...)
	if code.count() != len(want) || len(code.located) != 0 {
		t.Fatalf("packed %d of %d instructions", len(code.packed), len(want))
	}
	extras := 0
	for n, w := range want {
		if needsExtra(&w.ins) {
			extras++
		}
		if got := code.at(n); !sameInstruction(got, w.ins) {
			t.Fatalf("instruction %d = %+v; want %+v", n, got, w.ins)
		}
		if code.opAt(n) != w.ins.Op || code.pcAt(n) != w.pc {
			t.Fatalf("instruction %d: op %v pc %d; want %v %d", n, code.opAt(n), code.pcAt(n), w.ins.Op, w.pc)
		}
	}
	if len(code.extra) != extras {
		t.Fatalf("%d extras for %d instructions that need one", len(code.extra), extras)
	}
	// The located form reads back the same.
	located := physicalCode{located: slices.Clone(want)}
	for n, w := range want {
		if !sameInstruction(located.at(n), w.ins) || located.opAt(n) != w.ins.Op || located.pcAt(n) != w.pc {
			t.Fatalf("located instruction %d differs", n)
		}
	}
}

// decodeFrom, floatProbesFrom and spillSlots read the instructions at
// equal, in both forms, whatever batch they fill.
func TestPhysicalCodeBatchReads(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	var want []locatedInsn
	for n := range 3*decodeBatch + 5 {
		ins := randomInstruction(r)
		switch n % 4 {
		case 0:
			ins.Op = []machine.Opcode{pSpill, pReload, machine.OpFloatSpill, machine.OpVectorReload, pSaveCFloat, machine.OpX87Result}[r.Intn(6)]
			ins.Slot = r.Intn(40)
		case 1:
			ins.Op = []machine.Opcode{machine.OpFloatBinary, machine.OpFloatMove, machine.OpIntToFloat, machine.OpFloatFMA}[r.Intn(4)]
		}
		want = append(want, locatedInsn{ins: ins, pc: n})
	}
	architectures := []string{"amd64", "arm64", "386", "riscv64", "ppc64le", "loong64"}
	for _, code := range []physicalCode{codeOf(want...), {located: slices.Clone(want)}} {
		for _, size := range []int{1, 3, decodeBatch} {
			batch := make([]machine.Instruction, size)
			for n := 0; n < code.count(); {
				for i := range batch {
					batch[i] = randomInstruction(r)
				}
				insns := code.decodeFrom(n, batch)
				if len(insns) != min(size, code.count()-n) {
					t.Fatalf("decodeFrom(%d) returned %d instructions", n, len(insns))
				}
				for i := range insns {
					if !sameInstruction(insns[i], want[n+i].ins) {
						t.Fatalf("decodeFrom(%d)[%d] = %+v; want %+v", n, i, insns[i], want[n+i].ins)
					}
				}
				n += len(insns)
			}
			probes := make([]machine.Instruction, size)
			for n := 0; n < code.count(); n += size {
				code.floatProbesFrom(n, probes)
				for i := range min(size, code.count()-n) {
					w := want[n+i].ins
					probe := machine.Instruction{Op: w.Op, Bits: w.Bits, Arithmetic: w.Arithmetic, A: w.A, B: w.B, C: w.C, D: w.D}
					if !reflect.DeepEqual(probes[i], probe) {
						t.Fatalf("floatProbesFrom(%d)[%d] = %+v; want %+v", n, i, probes[i], probe)
					}
					for _, arch := range architectures {
						if got, full := machine.FloatWrites(probes[i], arch), machine.FloatWrites(w, arch); !slices.Equal(got, full) {
							t.Fatalf("%s: FloatWrites of probe %+v = %v; of %+v %v", arch, probes[i], got, w, full)
						}
					}
				}
			}
		}
		insns := make([]machine.Instruction, len(want))
		for n := range want {
			insns[n] = want[n].ins
		}
		for _, word := range []uint8{32, 64} {
			if got, w := code.spillSlots(word), referencedSpillSlots(insns, word); got != w || w == 0 {
				t.Fatalf("spillSlots(%d) = %d; want %d", word, got, w)
			}
		}
	}
}

// fieldValues returns non-zero values of typ, and for slices both an empty
// and a nil one.
func fieldValues(typ reflect.Type) []reflect.Value {
	v := reflect.New(typ).Elem()
	switch typ.Kind() {
	case reflect.Struct:
		var values []reflect.Value
		for i := range typ.NumField() {
			for _, field := range fieldValues(typ.Field(i).Type) {
				s := reflect.New(typ).Elem()
				s.Field(i).Set(field)
				values = append(values, s)
			}
		}
		return values
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int:
		w := reflect.New(typ).Elem()
		w.SetInt(math.MinInt)
		v.SetInt(math.MaxInt)
		return []reflect.Value{v, w}
	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(math.MaxUint64)
	case reflect.String:
		v.SetString("symbol")
	case reflect.Slice:
		return []reflect.Value{reflect.MakeSlice(typ, 0, 0), reflect.MakeSlice(typ, 2, 3), v}
	default:
		panic("unhandled field kind " + typ.Kind().String())
	}
	return []reflect.Value{v}
}

// set, setPC, setDisp, reset and add keep the packed program equal to a
// plain list of located instructions, with at most one extra for each.
func TestPhysicalCodeMutations(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for round := range 200 {
		var code physicalCode
		var want []locatedInsn
		for step := range 64 {
			switch k := r.Intn(8); {
			case k < 3 || len(want) == 0:
				ins := randomInstruction(r)
				pc := r.Int()
				code.add(&ins, pc)
				want = append(want, locatedInsn{ins: ins, pc: pc})
			case k < 5:
				n, ins, pc := r.Intn(len(want)), randomInstruction(r), r.Int()
				code.set(n, &ins, pc)
				want[n] = locatedInsn{ins: ins, pc: pc}
			case k == 5:
				n, pc := r.Intn(len(want)), r.Int()
				code.setPC(n, pc)
				want[n].pc = pc
			case k == 6:
				n, disp := r.Intn(len(want)), r.Uint64()
				code.setDisp(n, disp)
				want[n].ins.Disp = disp
			default:
				if r.Intn(8) == 0 {
					code.reset()
					want = want[:0]
				}
			}
			if code.count() != len(want) {
				t.Fatalf("round %d step %d: %d instructions; want %d", round, step, code.count(), len(want))
			}
			for n, w := range want {
				if !sameInstruction(code.at(n), w.ins) || code.pcAt(n) != w.pc || code.opAt(n) != w.ins.Op {
					t.Fatalf("round %d step %d: instruction %d differs", round, step, n)
				}
			}
			if len(code.extra) > len(code.packed) {
				t.Fatalf("round %d step %d: %d extras for %d instructions", round, step, len(code.extra), len(code.packed))
			}
		}
	}
}

// Every target's GoFrame describes the same frame from goFrameCode's
// instructions as from the whole program.
func TestGoFrameCodeMatchesWholeProgram(t *testing.T) {
	targets := map[string]func([]machine.LocatedInstruction, int, int) (goregister.Frame, error){
		"amd64": targetamd64.GoFrame, "arm64": targetarm64.GoFrame, "386": targeti386.GoFrame,
		"riscv64": targetriscv64.GoFrame, "loong64": targetloong64.GoFrame, "ppc64le": targetppc64.GoFrame,
	}
	const codeBytes = 1 << 16
	body := func(pc int) []locatedInsn {
		return []locatedInsn{
			{ins: machine.Instruction{Op: machine.OpConst, A: 1, Imm: 7}, pc: pc},
			{ins: machine.Instruction{Op: machine.OpCallGo, Symbol: "callee"}, pc: pc + 16},
			{ins: machine.Instruction{Op: machine.OpSpill, A: 1, Slot: 2}, pc: pc + 32},
		}
	}
	program := func(parts ...[]locatedInsn) []locatedInsn {
		var insns []locatedInsn
		for _, part := range parts {
			insns = append(insns, part...)
		}
		return insns
	}
	enter := func(frame, pc int) []locatedInsn {
		return []locatedInsn{{ins: machine.Instruction{Op: machine.OpFrameEnter, Imm: uint64(frame), CallAreaBytes: 16}, pc: pc}}
	}
	leave := func(frame, pc int) []locatedInsn {
		return []locatedInsn{{ins: machine.Instruction{Op: machine.OpFrameLeave, Imm: uint64(frame)}, pc: pc}}
	}
	ret := func(pc int) []locatedInsn {
		return []locatedInsn{{ins: machine.Instruction{Op: machine.OpReturn}, pc: pc}}
	}
	type testCase struct {
		frame int
		insns []locatedInsn
	}
	var cases []testCase
	for _, frame := range []int{0, 64, 4096, 8192} {
		cases = append(cases,
			// A leaf return ends the program.
			testCase{frame, program(body(0), ret(64))},
			// A framed function with code after its return.
			testCase{frame, program(enter(frame, 0), body(64), leave(frame, 128), ret(192), body(196))},
			// Two exits, the last ending the program.
			testCase{frame, program(enter(frame, 0), body(64), leave(frame, 128), ret(192), body(196), leave(frame, 260), ret(324))},
			// A return followed only by ignored instructions.
			testCase{frame, program(enter(frame, 0), leave(frame, 128), ret(192), body(196), body(260))},
			// Frame operations that do not match the frame.
			testCase{frame, program(enter(frame+16, 0), body(64), leave(frame, 128), ret(192))},
			testCase{frame, program(body(0), leave(frame, 128), ret(192))},
			// An empty program.
			testCase{frame, nil},
		)
	}
	for arch, goFrame := range targets {
		framed := 0
		for n, c := range cases {
			code := codeOf(c.insns...)
			whole := make([]machine.LocatedInstruction, len(c.insns))
			for i, located := range c.insns {
				whole[i] = machine.LocatedInstruction{Instruction: located.ins, PC: located.pc}
			}
			want, wantErr := goFrame(whole, codeBytes, c.frame)
			for _, form := range []physicalCode{code, {located: c.insns}} {
				got, err := goFrame(goFrameCode(&form), codeBytes, c.frame)
				if !reflect.DeepEqual(got, want) || (err == nil) != (wantErr == nil) || err != nil && err.Error() != wantErr.Error() {
					t.Fatalf("%s case %d: frame %+v, %v; want %+v, %v", arch, n, got, err, want, wantErr)
				}
			}
			if wantErr == nil && c.frame != 0 {
				framed++
			}
		}
		if framed == 0 {
			t.Fatalf("%s: no case built a frame", arch)
		}
	}
}
