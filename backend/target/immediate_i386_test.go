package target_test

import (
	"bytes"
	"fmt"
	"strconv"
	"testing"

	"golang.org/x/arch/x86/x86asm"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
)

// i386ShortImmediate reports whether imm is a sign-extended byte.
func i386ShortImmediate(imm uint32) bool { return int32(imm) >= -128 && int32(imm) <= 127 }

// i386ImmediateLength is the length of the shortest group-1 `op r32, imm`.
func i386ImmediateLength(reg backend.Register, imm uint32) int {
	switch {
	case i386ShortImmediate(imm):
		return 3
	case reg == 0:
		return 5
	}
	return 6
}

// TestI386ShortImmediates encodes each group-1 immediate form across the
// imm8 boundaries with both assemblers and decodes it: the operation,
// register and 32-bit immediate are kept, and the encoding is the shortest.
func TestI386ShortImmediates(t *testing.T) {
	imms := []uint64{^uint64(128), ^uint64(127), ^uint64(0), 0, 1, 127, 128, 0x7fffffff, 0x80000000, 0xffffffff}
	stack := abi.StackPlatform
	type immediateCase struct {
		ins  machine.Instruction
		op   x86asm.Op
		reg  backend.Register
		imm  uint32
		skip int // bytes before the immediate instruction
		rest int // bytes after it
	}
	var cases []immediateCase
	for _, imm := range imms {
		for _, arith := range []struct {
			arith op.Opcode
			op    x86asm.Op
		}{{op.OpAdd, x86asm.ADD}, {op.OpOr, x86asm.OR}, {op.OpAnd, x86asm.AND}, {op.OpSub, x86asm.SUB}, {op.OpXor, x86asm.XOR}} {
			for _, a := range []backend.Register{0, 3, 4, 7} {
				for _, b := range []backend.Register{a, 1} {
					skip := 0
					if a != b {
						skip = 2
					}
					cases = append(cases, immediateCase{machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: arith.arith, Bits: 32, A: a, B: b, Imm: imm}, arith.op, a, uint32(imm), skip, 0})
				}
			}
		}
		for _, a := range []backend.Register{0, 2, 6} {
			cases = append(cases, immediateCase{machine.Instruction{Op: machine.OpCompareImmediate, A: a, Imm: imm}, x86asm.CMP, a, uint32(imm), 0, 0})
		}
		if adjust := uint32(imm); adjust <= 1<<20-16 {
			frame := uint64(adjust) + 16
			cases = append(cases,
				immediateCase{machine.Instruction{Op: machine.OpFrameEnter, Imm: frame}, x86asm.SUB, 4, adjust, 4, 0},
				immediateCase{machine.Instruction{Op: machine.OpFrameEnter, Imm: frame, CallAreaBytes: 16}, x86asm.SUB, 4, adjust, 4, 0},
				immediateCase{machine.Instruction{Op: machine.OpFrameLeave, Imm: frame}, x86asm.ADD, 4, adjust, 0, 4},
				immediateCase{machine.Instruction{Op: machine.OpTailCall, Imm: frame}, x86asm.ADD, 4, adjust, 0, 6},
			)
		}
	}
	for _, c := range cases {
		where := fmt.Sprintf("%v %v A=%d B=%d Imm=%#x", c.ins.Op, c.ins.Arithmetic, c.ins.A, c.ins.B, c.ins.Imm)
		byteAssembler := i386.NewByteAssembler(stack)
		if err := machine.Visit(c.ins, byteAssembler); err != nil || byteAssembler.Err() != nil {
			t.Fatalf("%s: %v %v", where, err, byteAssembler.Err())
		}
		textAssembler := i386.NewTextAssembler(stack)
		if err := machine.Visit(c.ins, textAssembler); err != nil || textAssembler.Err() != nil {
			t.Fatalf("%s: text: %v %v", where, err, textAssembler.Err())
		}
		code := byteAssembler.Bytes()
		if !bytes.Equal(code, textAssembler.Bytes()) {
			t.Errorf("%s: byte assembler % x, text assembler % x", where, code, textAssembler.Bytes())
		}
		if size := i386.PhysicalSize(c.ins, stack, 0); size != len(code) {
			t.Errorf("%s: PhysicalSize %d, encoded %d", where, size, len(code))
		}
		want := i386ImmediateLength(c.reg, c.imm)
		if len(code) != c.skip+want+c.rest {
			t.Errorf("%s: % x is %d bytes, want %d", where, code, len(code), c.skip+want+c.rest)
			continue
		}
		inst, err := x86asm.Decode(code[c.skip:], 32)
		if err != nil {
			t.Fatalf("%s: decode % x: %v", where, code[c.skip:], err)
		}
		reg, _ := inst.Args[0].(x86asm.Reg)
		imm, _ := inst.Args[1].(x86asm.Imm)
		if inst.Op != c.op || reg != x86asm.EAX+x86asm.Reg(c.reg) || uint32(imm) != c.imm || inst.Len != want || inst.DataSize != 32 {
			t.Errorf("%s: % x decodes as %v (%d bytes), want %v %v, %#x", where, code[c.skip:c.skip+want], inst, inst.Len, c.op, x86asm.EAX+x86asm.Reg(c.reg), c.imm)
		}
	}
}

// TestI386ImmediatesShortest decodes the documented forms and their flag
// variants: every group-1 immediate takes its shortest encoding, the AND
// that masks an 8- or 16-bit result in EAX included.
func TestI386ImmediatesShortest(t *testing.T) {
	rows, err := readForms("i386/FORMS.md")
	if err != nil {
		t.Fatal(err)
	}
	assemble := formTargets(t)[2].assemble
	seen := map[string]bool{}
	checked := 0
	for _, row := range rows {
		if _, err := strconv.Atoi(row.bytes); err != nil || row.opcode == machine.OpLiteralData {
			continue
		}
		base, err := parseForm(row.opcode, row.example)
		if err != nil {
			t.Fatal(err)
		}
		for _, ins := range flagVariants(base) {
			key := fmt.Sprintf("%+v", ins)
			if seen[key] {
				continue
			}
			seen[key] = true
			code, err := assemble(ins)
			if err != nil {
				continue
			}
			for pc := 0; pc < len(code); {
				inst, err := x86asm.Decode(code[pc:], 32)
				if err != nil {
					t.Fatalf("i386/FORMS.md:%d: decode % x: %v", row.line, code[pc:], err)
				}
				opcode := code[pc]
				if opcode == 0x81 || opcode&0xc7 == 0x05 && opcode < 0x40 {
					checked++
					imm, _ := inst.Args[1].(x86asm.Imm)
					reg, isReg := inst.Args[0].(x86asm.Reg)
					switch {
					case i386ShortImmediate(uint32(imm)):
						t.Errorf("i386/FORMS.md:%d %v: %v takes imm32 for a byte immediate", row.line, ins.Op, inst)
					case opcode == 0x81 && isReg && reg == x86asm.EAX:
						t.Errorf("i386/FORMS.md:%d %v: %v takes 81 /n for EAX", row.line, ins.Op, inst)
					}
				}
				pc += inst.Len
			}
		}
	}
	if checked == 0 {
		t.Fatal("no imm32 group-1 instruction decoded")
	}
}
