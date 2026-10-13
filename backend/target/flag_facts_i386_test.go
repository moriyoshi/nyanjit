package target_test

import (
	"fmt"
	"strconv"
	"testing"

	"golang.org/x/arch/x86/x86asm"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
)

// x86FlagWrites is the S/Z/C subset of EFLAGS a decoded instruction may
// change, undefined results included. A shift by an immediate count that
// masks to zero changes none.
func x86FlagWrites(inst x86asm.Inst) machine.FlagMask {
	all := machine.FlagSign | machine.FlagZero | machine.FlagCarry
	switch inst.Op {
	case x86asm.SHL, x86asm.SHR, x86asm.SAR, x86asm.ROL, x86asm.ROR, x86asm.SHLD, x86asm.SHRD:
		for _, arg := range inst.Args {
			if count, ok := arg.(x86asm.Imm); ok && count&31 == 0 {
				return 0
			}
		}
	}
	switch inst.Op {
	case x86asm.ADD, x86asm.ADC, x86asm.SUB, x86asm.SBB, x86asm.CMP, x86asm.NEG, x86asm.AND, x86asm.OR, x86asm.XOR, x86asm.TEST,
		x86asm.IMUL, x86asm.MUL, x86asm.DIV, x86asm.IDIV, x86asm.SHL, x86asm.SHR, x86asm.SAR, x86asm.SHLD, x86asm.SHRD,
		x86asm.BSR, x86asm.BSF, x86asm.XADD, x86asm.CMPXCHG, x86asm.UCOMISS, x86asm.UCOMISD, x86asm.COMISS, x86asm.COMISD, x86asm.PTEST:
		return all
	case x86asm.INC, x86asm.DEC:
		return machine.FlagSign | machine.FlagZero
	case x86asm.BT: // SF is undefined, ZF unaffected
		return machine.FlagSign | machine.FlagCarry
	case x86asm.ROL, x86asm.ROR, x86asm.STC, x86asm.CLC, x86asm.CMC:
		return machine.FlagCarry
	case x86asm.CMPXCHG8B:
		return machine.FlagZero
	}
	return 0
}

// flagVariants varies the fields FlagFacts reads for the opcodes whose
// flags depend on them.
func flagVariants(base machine.Instruction) []machine.Instruction {
	out := []machine.Instruction{base}
	bits := func(values ...uint8) {
		n := len(out)
		for _, i := range out[:n] {
			for _, b := range values {
				i.Bits = b
				out = append(out, i)
			}
		}
	}
	imms := func(values ...uint64) {
		n := len(out)
		for _, i := range out[:n] {
			for _, v := range values {
				i.Imm = v
				out = append(out, i)
			}
		}
	}
	ariths := func(values ...op.Opcode) {
		n := len(out)
		for _, i := range out[:n] {
			for _, v := range values {
				i.Arithmetic = v
				out = append(out, i)
			}
		}
	}
	shifts := []op.Opcode{op.OpShiftLeft, op.OpShiftRight, op.OpShiftRightArithmetic, op.OpRotateLeft, op.OpRotateRight}
	switch base.Op {
	case machine.OpBinary:
		ariths(op.OpAdd, op.OpSub, op.OpAnd, op.OpOr, op.OpXor, op.OpMul)
		bits(0, 8, 16, 32)
	case machine.OpBinaryImmediate:
		ariths(op.OpAdd, op.OpSub, op.OpAnd, op.OpOr, op.OpXor)
		bits(0, 8, 16, 32)
		imms(0, 1, 0xff, 0xffffffff)
	case machine.OpNarrow, machine.OpSignExtend:
		imms(0, 1, 8, 16, 24, 32, 64)
	case machine.OpShift:
		ariths(shifts...)
		if base.Bits != 64 {
			bits(0, 8, 16, 24, 32)
		}
		imms(0, 1, 7, 8, 16, 31, 32, 33, 63, 64, 96, 255)
	case machine.OpShiftBy:
		ariths(shifts...)
		if base.Bits != 64 {
			bits(8, 16, 32)
		}
	case machine.OpBinaryCarry, machine.OpCarryBit, machine.OpWordCarry:
		ariths(op.OpAdd, op.OpSub)
		n := len(out)
		for _, i := range out[:n] {
			for mode := machine.CarryMode(0); mode <= machine.CarryFlags; mode++ {
				i.CarryMode = mode
				out = append(out, i)
			}
		}
	case machine.OpIntToFloat:
		ariths(op.OpFFromSigned, op.OpFFromUnsigned)
		bits(32, 64)
	case machine.OpFloatToInt:
		ariths(op.OpFToSigned, op.OpFToUnsigned)
		imms(0, 1)
		n := len(out)
		for _, i := range out[:n] {
			for _, d := range []uint64{32, 64} {
				i.Disp = d
				out = append(out, i)
			}
		}
	case machine.OpAtomic:
		ariths(op.OpAtomicLoad, op.OpAtomicStore, op.OpAtomicAdd, op.OpAtomicAnd, op.OpAtomicOr, op.OpAtomicXor, op.OpAtomicSwap, op.OpAtomicCompareSwap)
	case machine.OpReverseBytes:
		bits(16, 32)
	}
	return out
}

// TestI386FlagFactsMatchEncoding decodes every documented 386 form, and
// variants of the forms whose flags depend on their fields, and checks
// i386.FlagFacts against the instructions encoded: Modified is the union of
// the S, Z and C writes (calls clobber by contract), FlagEffect agrees, and
// each result fact names the last writer of its flag, an instruction on A
// of the fact's width.
func TestI386FlagFactsMatchEncoding(t *testing.T) {
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
			checked++
			where := fmt.Sprintf("i386/FORMS.md:%d %v %v Bits=%d Imm=%d Disp=%d Carry=%d", row.line, ins.Op, ins.Arithmetic, ins.Bits, ins.Imm, ins.Disp, ins.CarryMode)
			var union machine.FlagMask
			var last [3]x86asm.Inst // the last writer of S, Z and C
			branches := false
			for pc := 0; pc < len(code); {
				inst, err := x86asm.Decode(code[pc:], 32)
				if err != nil {
					t.Fatalf("%s: decode %x: %v", where, code[pc:], err)
				}
				m := x86FlagWrites(inst)
				union |= m
				for n := range last {
					if m&(1<<n) != 0 {
						last[n] = inst
					}
				}
				branches = branches || inst.PCRel != 0
				pc += inst.Len
			}
			facts := i386.FlagFacts(ins)
			want := union
			switch ins.Op {
			case machine.OpCallC, machine.OpCallGo, machine.OpSyscall, machine.OpTailCall:
				want = machine.FlagSign | machine.FlagZero | machine.FlagCarry
			}
			if facts.Modified != want {
				t.Errorf("%s: Modified %03b, encoding writes %03b", where, facts.Modified, union)
			}
			if i386.FlagEffect(ins) != (facts.Modified != 0) {
				t.Errorf("%s: FlagEffect disagrees with FlagFacts", where)
			}
			for n, f := range [3]machine.FlagFact{facts.Sign, facts.Zero, facts.Carry} {
				if !f.Valid {
					continue
				}
				inst := last[n]
				dst, _ := inst.Args[0].(x86asm.Reg)
				ok := !branches && facts.Modified&(1<<n) != 0 && dst == x86asm.EAX+x86asm.Reg(ins.A) && inst.DataSize == 32
				switch {
				case f.Width == 32:
					ok = ok && inst.Op != x86asm.BT
				case f.Width == 8 || f.Width == 16:
					// The narrow zero of AND A, mask.
					mask, _ := inst.Args[1].(x86asm.Imm)
					ok = ok && n == 1 && inst.Op == x86asm.AND && uint64(mask) == 1<<f.Width-1
				case f.Width == 64:
					// The sign and carry of a pair's high word.
					ok = ok && ins.Op == machine.OpBinaryCarry
				default:
					ok = false
				}
				switch {
				case ins.Op == machine.OpBinaryCarry:
					// The high word of a pair ADD/SUB: the pair's sign and
					// carry or borrow.
					ok = ok && n != 1 && (inst.Op == x86asm.ADC || inst.Op == x86asm.SBB)
				case n == 2:
					// Carry facts: the borrow or carry of ADD/SUB, or
					// IMUL's signed overflow.
					ok = ok && (inst.Op == x86asm.ADD || inst.Op == x86asm.SUB || inst.Op == x86asm.IMUL && len(inst.Args) >= 2 && inst.Args[1] != nil)
				}
				if !ok {
					t.Errorf("%s: fact %d (width %d) does not describe %v", where, n, f.Width, x86asm.IntelSyntax(inst, 0, nil))
				}
			}
		}
	}
	if checked < 1000 {
		t.Fatalf("checked only %d forms", checked)
	}
	t.Logf("checked %d forms", checked)
}
