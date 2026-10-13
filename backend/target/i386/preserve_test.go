package i386

import (
	"strings"
	"testing"

	"golang.org/x/arch/x86/x86asm"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// TestMaskForms encodes each PreserveFlags form whose 8- or 16-bit result
// is zero-extended by MOVZX with both assemblers and decodes it: the bytes,
// text and size agree, the mask is MOVZX, and FlagFacts reports the flags of
// the operation before it, without facts.
func TestMaskForms(t *testing.T) {
	const all = machine.FlagSign | machine.FlagZero | machine.FlagCarry
	binary := func(o op.Opcode, w uint8, a, b, c uint8) machine.Instruction {
		return machine.Instruction{Op: machine.OpBinary, Arithmetic: o, Bits: w, A: reg(a), B: reg(b), C: reg(c)}
	}
	and := func(imm uint64, a, b uint8) machine.Instruction {
		return machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAnd, Bits: 32, A: reg(a), B: reg(b), Imm: imm}
	}
	shift := func(o op.Opcode, w uint8, a, b uint8, n uint64) machine.Instruction {
		return machine.Instruction{Op: machine.OpShift, Arithmetic: o, Bits: w, A: reg(a), B: reg(b), Imm: n}
	}
	for _, tc := range []struct {
		ins      machine.Instruction
		text     string
		modified machine.FlagMask
	}{
		{binary(op.OpAdd, 8, 0, 1, 2), "lea EAX, [ECX + EDX*1 + 0]; movzx EAX, AL", 0},
		{binary(op.OpAdd, 16, 6, 5, 5), "lea ESI, [EBP + EBP*1 + 0]; movzx ESI, SI", 0},
		{binary(op.OpAdd, 8, 3, 3, 1), "add EBX, ECX; movzx EBX, BL", all},
		{binary(op.OpSub, 8, 1, 2, 3), "mov ECX, EDX; sub ECX, EBX; movzx ECX, CL", all},
		{binary(op.OpAnd, 16, 7, 7, 0), "and EDI, EAX; movzx EDI, DI", all},
		{binary(op.OpOr, 16, 5, 5, 6), "or EBP, ESI; movzx EBP, BP", all},
		{binary(op.OpXor, 8, 2, 2, 6), "xor EDX, ESI; movzx EDX, DL", all},
		{binary(op.OpMul, 8, 0, 0, 7), "imul EAX, EDI; movzx EAX, AL", all},
		{and(0xff, 0, 0), "movzx EAX, AL", 0},
		{and(0xff, 6, 3), "movzx ESI, BL", 0},
		{and(0xff, 1, 7), "mov ECX, EDI; movzx ECX, CL", 0},
		{and(0xffff, 7, 6), "movzx EDI, SI", 0},
		{shift(op.OpShiftLeft, 8, 0, 0, 3), "shl EAX, 3; movzx EAX, AL", all},
		{shift(op.OpShiftRight, 16, 6, 2, 5), "mov ESI, EDX; shr ESI, 5; movzx ESI, SI", all},
		{shift(op.OpRotateLeft, 16, 2, 2, 4), "rol EDX, 4; movzx EDX, DX", machine.FlagCarry},
		{shift(op.OpShiftLeft, 8, 3, 3, 32), "shl EBX, 32; movzx EBX, BL", 0},
	} {
		ins := tc.ins
		ins.PreserveFlags = true
		where := tc.text
		a := NewByteAssembler(abi.StackPlatform)
		if err := machine.Visit(ins, a); err != nil || a.Err() != nil {
			t.Fatalf("%s: %v %v", where, err, a.Err())
		}
		code := a.Bytes()
		text := NewTextAssembler(abi.StackPlatform)
		if err := machine.Visit(ins, text); err != nil || text.Err() != nil {
			t.Fatalf("%s: text: %v %v", where, err, text.Err())
		}
		if got := strings.ReplaceAll(strings.TrimSuffix(text.Text(), "\n"), "\n", "; "); got != tc.text || string(text.Bytes()) != string(code) {
			t.Errorf("%s: text %q, bytes % x and % x", where, got, text.Bytes(), code)
		}
		if size := PhysicalSize(ins, abi.StackPlatform, 0); size != len(code) {
			t.Errorf("%s: size %d, encoded %d", where, size, len(code))
		}
		if f := FlagFacts(ins); f.Modified != tc.modified || f.Sign.Valid || f.Zero.Valid || f.Carry.Valid {
			t.Errorf("%s: FlagFacts %+v", where, f)
		}
		var last x86asm.Inst
		for pc := 0; pc < len(code); pc += last.Len {
			inst, err := x86asm.Decode(code[pc:], 32)
			if err != nil {
				t.Fatalf("%s: decode % x: %v", where, code[pc:], err)
			}
			if _, imm := inst.Args[1].(x86asm.Imm); inst.Op == x86asm.AND && imm {
				t.Errorf("%s: % x masks with AND", where, code)
			}
			last = inst
		}
		dst, _ := last.Args[0].(x86asm.Reg)
		src, _ := last.Args[1].(x86asm.Reg)
		width := uint8(16)
		if src >= x86asm.AL && src <= x86asm.BL {
			width = 8
		}
		if last.Op != x86asm.MOVZX || dst != x86asm.EAX+x86asm.Reg(ins.A) || src == x86asm.AH || src == x86asm.CH || src == x86asm.DH || src == x86asm.BH || ins.Op != machine.OpBinaryImmediate && width != ins.Bits {
			t.Errorf("%s: ends with %v", where, last)
		}
	}
}

func reg(r uint8) backend.Register { return backend.Register(r) }

// TestElideDeadFlagsNarrows: a narrowing whose flags are dead becomes the
// PreserveFlags form of its AND, MOVZX, and keeps its AND when they are
// read or no byte register exists.
func TestElideDeadFlagsNarrows(t *testing.T) {
	branch := machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchEqual, Imm: 0}
	ret := machine.Instruction{Op: machine.OpReturn}
	for _, tc := range []struct {
		narrow machine.Instruction
		read   bool
		want   bool
	}{
		{machine.Instruction{Op: machine.OpNarrow, A: 0, B: 6, Imm: 16}, false, true},
		{machine.Instruction{Op: machine.OpNarrow, A: 6, B: 1, Imm: 8}, false, true},
		{machine.Instruction{Op: machine.OpNarrow, A: 6, B: 7, Imm: 8}, false, false},
		{machine.Instruction{Op: machine.OpNarrow, A: 3, B: 3, Imm: 8}, true, false},
		{machine.Instruction{Op: machine.OpNarrow, A: 3, B: 3, Imm: 32}, false, false},
	} {
		code := []machine.Instruction{tc.narrow, ret}
		labels, bound := []int{0}, []bool{false}
		if tc.read {
			code = []machine.Instruction{tc.narrow, branch, ret}
		}
		s := deadFlagStream(code...)
		if tc.read {
			labels[0], bound[0] = s.SizeAt(0)+s.SizeAt(1), true
		}
		if _, err := (Policy{}).ElideDeadFlags(s, labels, bound); err != nil {
			t.Fatal(err)
		}
		got := s.Instructions()[0]
		want := tc.narrow
		if tc.want {
			want = machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAnd, Bits: 32, A: tc.narrow.A, B: tc.narrow.B, Imm: 1<<tc.narrow.Imm - 1, PreserveFlags: true}
		}
		if got.Op != want.Op || got.Arithmetic != want.Arithmetic || got.Imm != want.Imm || got.PreserveFlags != want.PreserveFlags || got.A != want.A || got.B != want.B {
			t.Errorf("%+v: became %+v", tc.narrow, got)
		}
		if pc := s.SizeAt(0) + s.SizeAt(1); tc.want && pc != PhysicalSize(want, abi.StackPlatform, 0)+s.SizeAt(1) {
			t.Errorf("%+v: stream sizes %d", tc.narrow, pc)
		}
	}
}
