package target_test

import (
	"bufio"
	"embed"
	"encoding/hex"
	"fmt"
	"strconv"
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

// Each target documents its accepted machine.Instruction forms in FORMS.md.
// This test assembles every documented example with the target's byte
// assembler, checks the documented size against both the encoded bytes and
// the target's size function, and checks that rejected examples fail.

//go:embed */FORMS.md
var formDocuments embed.FS

type formTarget struct {
	dir      string
	assemble func(machine.Instruction) ([]byte, error)
	size     func(machine.Instruction) int
}

func visitTarget(newAssembler func() interface {
	machine.Assembler
	Bytes() []byte
	Err() error
}) func(machine.Instruction) ([]byte, error) {
	return func(ins machine.Instruction) ([]byte, error) {
		a := newAssembler()
		a.SetPC(0)
		if err := machine.Visit(ins, a); err != nil {
			return nil, err
		}
		if err := a.Err(); err != nil {
			return nil, err
		}
		return a.Bytes(), nil
	}
}

func formTargets(t *testing.T) []formTarget {
	stack := abi.StackPlatform
	profile, err := abi.NewPPC64Profile("ppc64le", abi.C)
	if err != nil {
		t.Fatal(err)
	}
	emitter, err := ppc64.NewEmitter(profile, stack)
	if err != nil {
		t.Fatal(err)
	}
	type assembler = interface {
		machine.Assembler
		Bytes() []byte
		Err() error
	}
	return []formTarget{
		{"amd64", visitTarget(func() assembler { return amd64.NewByteAssembler(stack, 0) }),
			func(i machine.Instruction) int { return amd64.PhysicalSize(i, stack, 0) }},
		{"arm64", visitTarget(func() assembler { return arm64.NewByteAssembler(stack, 0) }),
			func(i machine.Instruction) int { return arm64.PhysicalSize(i, stack, 0) }},
		{"i386", visitTarget(func() assembler { return i386.NewByteAssembler(stack) }),
			func(i machine.Instruction) int { return i386.PhysicalSize(i, stack, 0) }},
		{"loong64", visitTarget(func() assembler { return loong64.NewByteAssembler(stack, 0) }),
			func(i machine.Instruction) int { return loong64.PhysicalSize(i, stack, 0) }},
		{"riscv64", visitTarget(func() assembler { return riscv64.NewByteAssembler(stack, 0) }),
			func(i machine.Instruction) int { return riscv64.PhysicalSize(i, stack, 0) }},
		{"ppc64", func(ins machine.Instruction) ([]byte, error) {
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
		}, emitter.Size},
	}
}

// opcodeNames lists machine opcodes in declaration order.
var opcodeNames = []string{
	"OpConst", "OpSymbol", "OpMove", "OpBinary", "OpBinaryImmediate", "OpBinaryCarry",
	"OpInc", "OpDec", "OpNarrow", "OpSignExtend", "OpShift", "OpScaled", "OpMulAdd",
	"OpSelect", "OpSelectFlags", "OpLoad64", "OpStore64", "OpLoad8", "OpStore8",
	"OpLoadAddress", "OpStoreAddress", "OpLoadOffset", "OpStoreOffset", "OpSyscall",
	"OpCallC", "OpCallGo", "OpSaveLink", "OpRestoreLink", "OpSpill", "OpReload",
	"OpLoadIncoming", "OpFrameEnter", "OpFrameLeave", "OpReturn", "OpCompareImmediate",
	"OpCompareRegister", "OpJump", "OpJumpIf", "OpBranchZero", "OpBranchBit", "OpLoop",
	"OpMulWide", "OpSaveCFloat", "OpRestoreCFloat", "OpSaveCVector", "OpRestoreCVector",
	"OpSaveCondition", "OpRestoreCondition", "OpFloatBinary", "OpFloatFMA", "OpFloatUnary",
	"OpSetCount", "OpShiftBy", "OpMulHigh", "OpCarryBit", "OpWordCarry", "OpVector",
	"OpVectorMove", "OpVectorSpill", "OpVectorReload", "OpCountZeros", "OpAndNot",
	"OpFloatMove", "OpLiteralLoad", "OpLiteralData", "OpFloatSpill", "OpFloatReload",
	"OpFloatBits", "OpLoad16", "OpStore16", "OpLoad32", "OpStore32", "OpBarrier",
	"OpDivide", "OpIntToFloat", "OpFloatToInt", "OpMulHighSigned", "OpFloatCompare",
}

func TestOpcodeNamesCoverMachine(t *testing.T) {
	if !machine.Opcode(len(opcodeNames)-1).Valid() || machine.Opcode(len(opcodeNames)).Valid() {
		t.Fatalf("opcodeNames has %d entries; update it with machine.Opcode", len(opcodeNames))
	}
	if machine.OpFloatCompare != machine.Opcode(len(opcodeNames)-1) || machine.OpFrameEnter != 31 {
		t.Fatal("opcodeNames order differs from machine.Opcode")
	}
}

var barrierNames = map[string]machine.Barrier{
	"Speculation": machine.BarrierSpeculation,
	"Memory":      machine.BarrierMemory,
	"Instruction": machine.BarrierInstruction,
}

var conditionNames = map[string]op.BranchCondition{
	"Equal": op.BranchEqual, "NotEqual": op.BranchNotEqual, "Less": op.BranchLess,
	"GreaterEqual": op.BranchGreaterEqual, "LessEqual": op.BranchLessEqual, "Greater": op.BranchGreater,
	"Below": op.BranchBelow, "AboveEqual": op.BranchAboveEqual, "BelowEqual": op.BranchBelowEqual,
	"Above": op.BranchAbove, "Sign": machine.BranchSign, "NotSign": machine.BranchNotSign,
	"Carry": machine.BranchCarry, "NotCarry": machine.BranchNotCarry,
	"Carry32": machine.BranchCarry32, "NotCarry32": machine.BranchNotCarry32,
}

var comparisonNames = map[string]machine.ComparisonMode{
	"Equality": machine.CompareEquality, "Signed": machine.CompareSigned, "Unsigned": machine.CompareUnsigned,
}

func arithmeticByName(name string) (op.Opcode, bool) {
	for code := op.Opcode(0); code < 255; code++ {
		if code.String() == name {
			return code, true
		}
	}
	return 0, false
}

// parseForm reads an example such as "A=0 B=0 C=1 Bits=64 Arith=OpAdd".
func parseForm(opcode machine.Opcode, text string) (machine.Instruction, error) {
	ins := machine.Instruction{Op: opcode}
	if text == "" {
		return ins, nil
	}
	for _, field := range strings.Fields(text) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			switch field {
			case "Long":
				ins.Long = true
			case "Far":
				ins.Far = true
			case "Indexed":
				ins.Indexed = true
			case "PreserveFlags":
				ins.PreserveFlags = true
			case "BigEndian":
				ins.ByteOrder = op.ByteOrderBigEndian
			default:
				return ins, fmt.Errorf("unknown flag %q", field)
			}
			continue
		}
		var number uint64
		var err error
		parse := func() {
			number, err = strconv.ParseUint(value, 0, 64)
		}
		switch key {
		case "A", "B", "C", "D", "E", "HighA", "HighB", "HighC", "Bits", "Imm", "Disp", "Slot", "CallArea", "Carry":
			parse()
		case "SImm":
			var signed int64
			signed, err = strconv.ParseInt(value, 0, 64)
			number = uint64(signed)
		case "V":
			// Vector register number for A, B, C: V=A:1,B:2.
			for _, part := range strings.Split(value, ",") {
				reg, n, ok := strings.Cut(part, ":")
				if !ok {
					return ins, fmt.Errorf("bad vector register %q", part)
				}
				num, perr := strconv.ParseUint(n, 0, 8)
				if perr != nil {
					return ins, perr
				}
				r := backend.VectorRegister(uint8(num))
				switch reg {
				case "A":
					ins.A = r
				case "B":
					ins.B = r
				case "C":
					ins.C = r
				case "D":
					ins.D = r
				default:
					return ins, fmt.Errorf("bad vector operand %q", reg)
				}
			}
			continue
		}
		if err != nil {
			return ins, fmt.Errorf("%s: %w", field, err)
		}
		switch key {
		case "A":
			ins.A = backend.Register(number)
		case "B":
			ins.B = backend.Register(number)
		case "C":
			ins.C = backend.Register(number)
		case "D":
			ins.D = backend.Register(number)
		case "E":
			ins.E = backend.Register(number)
		case "HighA":
			ins.HighA = backend.Register(number)
		case "HighB":
			ins.HighB = backend.Register(number)
		case "HighC":
			ins.HighC = backend.Register(number)
		case "Bits":
			ins.Bits = uint8(number)
		case "Imm", "SImm":
			ins.Imm = number
		case "Disp":
			ins.Disp = number
		case "Slot":
			ins.Slot = int(number)
		case "CallArea":
			ins.CallAreaBytes = int(number)
		case "Carry":
			ins.CarryMode = machine.CarryMode(number)
		case "Arith":
			code, ok := arithmeticByName(value)
			if !ok {
				return ins, fmt.Errorf("unknown arithmetic %q", value)
			}
			ins.Arithmetic = code
		case "Cond":
			condition, ok := conditionNames[value]
			if !ok {
				return ins, fmt.Errorf("unknown condition %q", value)
			}
			ins.Condition = condition
		case "Cmp":
			mode, ok := comparisonNames[value]
			if !ok {
				return ins, fmt.Errorf("unknown comparison %q", value)
			}
			ins.Comparison = mode
		case "Barrier":
			barrier, ok := barrierNames[value]
			if !ok {
				return ins, fmt.Errorf("unknown barrier %q", value)
			}
			ins.Barrier = barrier
		case "Sym":
			ins.Symbol = value
		case "Data":
			data, derr := hex.DecodeString(value)
			if derr != nil {
				return ins, derr
			}
			ins.Data = data
		case "Vec":
			// Vec=KxN: K lanes of N bits; Vec=sN: one N-bit scalar.
			var vt abi.ValueType
			if bits, ok := strings.CutPrefix(value, "s"); ok {
				n, perr := strconv.ParseUint(bits, 10, 8)
				if perr != nil {
					return ins, perr
				}
				vt = abi.ValueType{Kind: abi.ScalarKind, Lanes: 1, LaneBits: uint8(n)}
			} else {
				lanes, bits, ok := strings.Cut(value, "x")
				if !ok {
					return ins, fmt.Errorf("bad vector type %q", value)
				}
				l, perr := strconv.ParseUint(lanes, 10, 16)
				if perr != nil {
					return ins, perr
				}
				b, perr := strconv.ParseUint(bits, 10, 8)
				if perr != nil {
					return ins, perr
				}
				vt = abi.ValueType{Kind: abi.VectorKind, Lanes: uint16(l), LaneBits: uint8(b)}
			}
			ins.VectorType = vt
		default:
			return ins, fmt.Errorf("unknown field %q", key)
		}
	}
	return ins, nil
}

type formRow struct {
	line    int
	opcode  machine.Opcode
	example string
	bytes   string // a decimal size, "rejected" or "unsupported"
}

func readForms(path string) ([]formRow, error) {
	file, err := formDocuments.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	byName := map[string]machine.Opcode{}
	for i, name := range opcodeNames {
		byName[name] = machine.Opcode(i)
	}
	var rows []formRow
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := scanner.Text()
		if !strings.HasPrefix(text, "| `Op") {
			continue
		}
		cells := strings.Split(text, "|")
		if len(cells) < 5 {
			return nil, fmt.Errorf("line %d: want | opcode | example | bytes | notes |", line)
		}
		name := strings.Trim(strings.TrimSpace(cells[1]), "`")
		opcode, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("line %d: unknown opcode %q", line, name)
		}
		example := strings.TrimSpace(cells[2])
		if example == "—" {
			example = ""
		}
		rows = append(rows, formRow{line: line, opcode: opcode, example: strings.Trim(example, "`"), bytes: strings.TrimSpace(cells[3])})
	}
	return rows, scanner.Err()
}

func TestDocumentedForms(t *testing.T) {
	for _, target := range formTargets(t) {
		t.Run(target.dir, func(t *testing.T) {
			path := target.dir + "/FORMS.md"
			rows, err := readForms(path)
			if err != nil {
				t.Fatal(err)
			}
			seen := make([]bool, len(opcodeNames))
			for _, row := range rows {
				seen[row.opcode] = true
				where := fmt.Sprintf("%s:%d %s `%s`", path, row.line, opcodeNames[row.opcode], row.example)
				ins, err := parseForm(row.opcode, row.example)
				if err != nil {
					t.Errorf("%s: %v", where, err)
					continue
				}
				code, err := target.assemble(ins)
				switch row.bytes {
				case "rejected", "unsupported":
					if err == nil {
						t.Errorf("%s: documented as %s, but assembled %d bytes", where, row.bytes, len(code))
					}
					continue
				}
				want, perr := strconv.Atoi(row.bytes)
				if perr != nil {
					t.Errorf("%s: bytes column %q is not a size, \"rejected\" or \"unsupported\"", where, row.bytes)
					continue
				}
				if err != nil {
					t.Errorf("%s: documented as %d bytes, but assembly failed: %v", where, want, err)
					continue
				}
				if len(code) != want {
					t.Errorf("%s: documented as %d bytes, assembled %d", where, want, len(code))
				}
				if size := target.size(ins); size != len(code) {
					t.Errorf("%s: size function reports %d bytes, assembled %d", where, size, len(code))
				}
			}
			for opcode, ok := range seen {
				if !ok {
					t.Errorf("%s does not document %s", path, opcodeNames[opcode])
				}
			}
		})
	}
}
