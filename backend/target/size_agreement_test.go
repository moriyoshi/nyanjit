package target_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/amd64"
	"github.com/moriyoshi/nyanjit/backend/target/arm64"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
	"github.com/moriyoshi/nyanjit/backend/target/riscv64"
)

// fieldMutation stores a random value, valid or not, in one Instruction field.
type fieldMutation struct {
	name   string
	mutate func(*machine.Instruction, *rand.Rand)
}

func randomRegister(r *rand.Rand) backend.Register {
	switch r.Intn(5) {
	case 0:
		return backend.Register(r.Intn(16))
	case 1:
		return backend.Register(r.Intn(40))
	case 2:
		return backend.VectorRegister(uint8(r.Intn(40)))
	case 3:
		return backend.Register(r.Intn(256))
	}
	return backend.Register(r.Intn(8))
}

func randomWord(r *rand.Rand) uint64 {
	switch r.Intn(4) {
	case 0:
		values := []uint64{0, 1, 2, 3, 4, 7, 8, 15, 16, 31, 32, 63, 64, 65, 127, 128, 255, 256, 4095, 4096,
			0x7fff, 0x8000, 0xffff, 0x10000, 1 << 20, 1<<20 + 16, 0x7fffffff, 0x80000000, 0xffffffff, 1 << 32, 1 << 63, ^uint64(0)}
		return values[r.Intn(len(values))]
	case 1:
		return uint64(-int64(r.Intn(5000) + 1))
	case 2:
		return uint64(r.Intn(130))
	}
	return r.Uint64()
}

const maxInt = int(^uint(0) >> 1)

var fieldMutations = []fieldMutation{
	{"A", func(i *machine.Instruction, r *rand.Rand) { i.A = randomRegister(r) }},
	{"B", func(i *machine.Instruction, r *rand.Rand) { i.B = randomRegister(r) }},
	{"C", func(i *machine.Instruction, r *rand.Rand) { i.C = randomRegister(r) }},
	{"D", func(i *machine.Instruction, r *rand.Rand) { i.D = randomRegister(r) }},
	{"E", func(i *machine.Instruction, r *rand.Rand) { i.E = randomRegister(r) }},
	{"HighA", func(i *machine.Instruction, r *rand.Rand) { i.HighA = randomRegister(r) }},
	{"HighB", func(i *machine.Instruction, r *rand.Rand) { i.HighB = randomRegister(r) }},
	{"HighC", func(i *machine.Instruction, r *rand.Rand) { i.HighC = randomRegister(r) }},
	{"Bits", func(i *machine.Instruction, r *rand.Rand) {
		values := []uint8{0, 1, 8, 16, 32, 64, 128, 7, 255}
		i.Bits = values[r.Intn(len(values))]
	}},
	{"Imm", func(i *machine.Instruction, r *rand.Rand) { i.Imm = randomWord(r) }},
	{"Disp", func(i *machine.Instruction, r *rand.Rand) { i.Disp = randomWord(r) }},
	{"Arithmetic", func(i *machine.Instruction, r *rand.Rand) { i.Arithmetic = op.Opcode(r.Intn(256)) }},
	{"Condition", func(i *machine.Instruction, r *rand.Rand) { i.Condition = op.BranchCondition(r.Intn(24)) }},
	{"Comparison", func(i *machine.Instruction, r *rand.Rand) { i.Comparison = machine.ComparisonMode(r.Intn(5)) }},
	{"ByteOrder", func(i *machine.Instruction, r *rand.Rand) { i.ByteOrder = op.ByteOrder(r.Intn(4)) }},
	{"Barrier", func(i *machine.Instruction, r *rand.Rand) { i.Barrier = machine.Barrier(r.Intn(6)) }},
	{"CarryMode", func(i *machine.Instruction, r *rand.Rand) { i.CarryMode = machine.CarryMode(r.Intn(6)) }},
	{"FlagRequest", func(i *machine.Instruction, r *rand.Rand) { i.FlagRequest = machine.FlagMask(r.Intn(256)) }},
	{"Slot", func(i *machine.Instruction, r *rand.Rand) {
		values := []int{-1, 0, 1, 2, 511, 4094, 4095, 4096, 1 << 20, 1 << 25, maxInt, r.Intn(10000)}
		i.Slot = values[r.Intn(len(values))]
	}},
	{"CallAreaBytes", func(i *machine.Instruction, r *rand.Rand) {
		values := []int{-16, 0, 8, 16, 4096, 1 << 21}
		i.CallAreaBytes = values[r.Intn(len(values))]
	}},
	{"Indexed", func(i *machine.Instruction, r *rand.Rand) { i.Indexed = !i.Indexed }},
	{"Long", func(i *machine.Instruction, r *rand.Rand) { i.Long = !i.Long }},
	{"Far", func(i *machine.Instruction, r *rand.Rand) { i.Far = !i.Far }},
	{"PreserveFlags", func(i *machine.Instruction, r *rand.Rand) { i.PreserveFlags = !i.PreserveFlags }},
	{"Symbol", func(i *machine.Instruction, r *rand.Rand) { i.Symbol = []string{"", "f", "callee"}[r.Intn(3)] }},
	{"Data", func(i *machine.Instruction, r *rand.Rand) {
		n := []int{0, 3, 4, 8, 16}[r.Intn(5)]
		i.Data = nil
		if n != 0 {
			i.Data = make([]byte, n)
			r.Read(i.Data)
		}
	}},
	{"VectorType", func(i *machine.Instruction, r *rand.Rand) {
		i.VectorType = []abi.ValueType{{}, {Kind: abi.ScalarKind, Lanes: 1, LaneBits: 64}, {Kind: abi.VectorKind, Lanes: 4, LaneBits: 32}}[r.Intn(3)]
	}},
}

// formFieldNames maps the keys of a FORMS.md example to Instruction fields.
var formFieldNames = map[string]string{
	"SImm": "Imm", "Arith": "Arithmetic", "Cond": "Condition", "Cmp": "Comparison", "Sym": "Symbol",
	"Vec": "VectorType", "CallArea": "CallAreaBytes", "Carry": "CarryMode", "Flags": "FlagRequest", "BigEndian": "ByteOrder",
}

// formFields returns the Instruction fields an example names.
func formFields(example string) []string {
	var fields []string
	for _, field := range strings.Fields(example) {
		key, value, _ := strings.Cut(field, "=")
		if key == "V" {
			for _, part := range strings.Split(value, ",") {
				register, _, _ := strings.Cut(part, ":")
				fields = append(fields, register)
			}
			continue
		}
		if name, ok := formFieldNames[key]; ok {
			key = name
		}
		fields = append(fields, key)
	}
	return fields
}

// pcTarget names the field that holds an absolute branch or literal PC. Its
// reach depends on where layout places the instruction, so a size function
// cannot reject it; the test keeps such targets near.
func pcTarget(opcode machine.Opcode) string {
	switch opcode {
	case machine.OpJump, machine.OpJumpIf, machine.OpBranchZero, machine.OpBranchBit, machine.OpLoop:
		return "Imm"
	case machine.OpLiteralLoad:
		return "Disp"
	case machine.OpJumpTable:
		return "Table"
	}
	return ""
}

func nearTarget(ins *machine.Instruction) {
	switch pcTarget(ins.Op) {
	case "Imm":
		ins.Imm = 64
	case "Disp":
		ins.Disp = 64
	case "Table":
		table := make([]int, len(ins.Table))
		for i := range table {
			table[i] = 64 * (i + 1)
		}
		ins.Table = table
	}
}

type encoding struct {
	code []byte
	err  error
	size int
}

func (e encoding) String() string {
	if e.err != nil {
		return fmt.Sprintf("size %d, error %v", e.size, e.err)
	}
	return fmt.Sprintf("size %d, bytes % x", e.size, e.code)
}

// agreementTarget is a form target under one stack, Go home and CPU setup.
type agreementTarget struct {
	name string
	formTarget
}

// agreementTargets adds the other stack practices, Go argument homes and
// optional CPU extensions to the documented setups of formTargets.
func agreementTargets(t *testing.T) []agreementTarget {
	type assembler = interface {
		machine.Assembler
		Bytes() []byte
		Err() error
	}
	resolve := func(architecture string, profile abi.CPUProfile) abi.ResolvedCPU {
		cpu, err := profile.Resolved(architecture)
		if err != nil {
			t.Fatal(err)
		}
		return cpu
	}
	amd64CPU := resolve("amd64", abi.CPUProfile{Level: "x86-64-v4", Extensions: []string{"pclmulqdq"}})
	riscv64CPU := resolve("riscv64", abi.CPUProfile{Level: "rva23u64"})
	var targets []agreementTarget
	for _, target := range formTargets(t) {
		targets = append(targets, agreementTarget{target.dir, target})
	}
	for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
		for _, homes := range []int{0, 9} {
			name := fmt.Sprintf("/stack%d-homes%d", stack, homes)
			targets = append(targets,
				agreementTarget{"amd64" + name + "-v4", formTarget{"amd64",
					visitTarget(func() assembler { return amd64.NewByteAssembler(stack, homes).WithCPU(amd64CPU) }),
					func(i machine.Instruction) int { return amd64.PhysicalSizeWithCPU(i, stack, amd64CPU, homes) }}},
				agreementTarget{"arm64" + name, formTarget{"arm64",
					visitTarget(func() assembler { return arm64.NewByteAssembler(stack, homes) }),
					func(i machine.Instruction) int { return arm64.PhysicalSize(i, stack, homes) }}},
				agreementTarget{"loong64" + name, formTarget{"loong64",
					visitTarget(func() assembler { return loong64.NewByteAssembler(stack, homes) }),
					func(i machine.Instruction) int { return loong64.PhysicalSize(i, stack, homes) }}},
				agreementTarget{"riscv64" + name + "-rva23", formTarget{"riscv64",
					visitTarget(func() assembler { return riscv64.NewByteAssembler(stack, homes).WithCPU(riscv64CPU) }),
					func(i machine.Instruction) int { return riscv64.PhysicalSizeWithCPU(i, stack, riscv64CPU, homes) }}},
			)
		}
		targets = append(targets, agreementTarget{fmt.Sprintf("i386/stack%d", stack), formTarget{"i386",
			visitTarget(func() assembler { return i386.NewByteAssembler(stack) }),
			func(i machine.Instruction) int { return i386.PhysicalSize(i, stack, 0) }}})
		for _, cpu := range []abi.PPC64CPU{abi.PPC64Power9, abi.PPC64Power10} {
			profile, err := abi.NewPPC64Profile("ppc64le", abi.Go)
			if err != nil {
				t.Fatal(err)
			}
			emitter, err := ppc64.NewEmitterForCPU(profile, stack, true, cpu)
			if err != nil {
				t.Fatal(err)
			}
			targets = append(targets, agreementTarget{fmt.Sprintf("ppc64/stack%d-power%d", stack, cpu), formTarget{"ppc64",
				func(ins machine.Instruction) ([]byte, error) {
					if ins.Op == machine.OpLiteralData {
						a := ppc64.NewByteAssembler()
						if err := a.Data(ins.Data); err != nil {
							return nil, err
						}
						return a.Bytes(), nil
					}
					sequence, err := emitter.Expand(ins, 0)
					if err != nil {
						return nil, err
					}
					a := ppc64.NewByteAssembler()
					for _, native := range sequence {
						if err := a.Visit(native); err != nil {
							return nil, err
						}
					}
					return a.Bytes(), nil
				}, emitter.Size}})
		}
	}
	return targets
}

// TestSizeAgreesWithEncoding mutates every documented form field by field.
// A size function reports zero bytes for exactly the instructions the
// encoder rejects and the encoded length for the others, and a field that
// the target's FORMS.md never names for an opcode changes neither. Byte
// order is common to every opcode: machine.Visit rejects a non-native order
// outside memory accesses.
func TestSizeAgreesWithEncoding(t *testing.T) {
	r := rand.New(rand.NewSource(16))
	rounds := 24
	if testing.Short() {
		rounds = 6
	}
	for _, target := range agreementTargets(t) {
		t.Run(target.name, func(t *testing.T) {
			rows, err := readForms(target.dir + "/FORMS.md")
			if err != nil {
				t.Fatal(err)
			}
			documented := make([]map[string]bool, len(opcodeNames))
			for _, row := range rows {
				if documented[row.opcode] == nil {
					documented[row.opcode] = map[string]bool{"ByteOrder": true}
				}
				for _, field := range formFields(row.example) {
					documented[row.opcode][field] = true
				}
			}
			failures := 0
			fail := func(format string, args ...any) {
				if failures++; failures <= 40 {
					t.Errorf(format, args...)
				}
			}
			agree := func(where string, ins machine.Instruction) encoding {
				code, err := target.assemble(ins)
				got := encoding{code: code, err: err, size: target.size(ins)}
				if err != nil && got.size != 0 || err == nil && got.size != len(code) {
					fail("%s: %v for %+v", where, got, ins)
				}
				return got
			}
			for _, row := range rows {
				base, err := parseForm(row.opcode, row.example)
				if err != nil {
					t.Fatal(err)
				}
				nearTarget(&base)
				where := fmt.Sprintf("%s `%s`", opcodeNames[row.opcode], row.example)
				want := agree(where, base)
				for _, m := range fieldMutations {
					if m.name == pcTarget(row.opcode) {
						continue
					}
					for range rounds {
						ins := base
						m.mutate(&ins, r)
						got := agree(where+" with "+m.name, ins)
						if !documented[row.opcode][m.name] && (got.size != want.size || (got.err == nil) != (want.err == nil) || !bytes.Equal(got.code, want.code)) {
							fail("%s: undocumented %s changes %v to %v for %+v", where, m.name, want, got, ins)
						}
					}
				}
				// Combine documented fields to reach forms no row names.
				for range rounds {
					ins := base
					for range 1 + r.Intn(3) {
						m := fieldMutations[r.Intn(len(fieldMutations))]
						if documented[row.opcode][m.name] && m.name != pcTarget(row.opcode) {
							m.mutate(&ins, r)
						}
					}
					agree(where+" mutated", ins)
				}
			}
			if failures > 40 {
				t.Errorf("%d failures in all", failures)
			}
		})
	}
}
