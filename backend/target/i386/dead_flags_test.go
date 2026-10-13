package i386

import (
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func deadFlagStream(code ...machine.Instruction) *machine.Stream {
	s := machine.NewStream(machine.StreamOptions{
		Size:           func(i machine.Instruction) int { return PhysicalSize(i, abi.StackPlatform, 0) },
		FlagEffect:     FlagEffect,
		FlagFacts:      FlagFacts,
		ExactFlagFacts: true,
	})
	for _, i := range code {
		s.Append(i)
	}
	return s
}

func TestElideDeadFlagsSelectsLEA(t *testing.T) {
	add := machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 32, A: 0, B: 7, C: 6}
	sub := machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpSub, Bits: 32, A: 1, B: 7, Imm: 8}
	inc := machine.Instruction{Op: machine.OpInc, A: 2, B: 6}
	carry := machine.Instruction{Op: machine.OpCarryBit, Bits: 32, Arithmetic: op.OpAdd, CarryMode: machine.CarryFlags, A: 3}
	compare := machine.Instruction{Op: machine.OpCompareRegister, Bits: 32, A: 7, B: 6}
	branch := machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchEqual, Imm: 0}
	overflow := machine.Instruction{Op: machine.OpJumpIf, Condition: machine.BranchOverflow, Imm: 0}
	noOverflow := machine.Instruction{Op: machine.OpJumpIf, Condition: machine.BranchNotOverflow, Imm: 0}
	selectOverflow := machine.Instruction{Op: machine.OpSelectFlags, Condition: machine.BranchOverflow, A: 3, C: 1, D: 2}
	// A pair's high word: ADC reads the CF of the low word's ADD.
	high := machine.Instruction{Op: machine.OpBinaryCarry, Arithmetic: op.OpAdd, A: 3, B: 5, C: 1}
	wordCarry := machine.Instruction{Op: machine.OpWordCarry, Arithmetic: op.OpAdd, Bits: 32, CarryMode: machine.CarryFlags, A: 3, B: 5, C: 1}
	move := machine.Instruction{Op: machine.OpMove, A: 5, B: 2}
	ret := machine.Instruction{Op: machine.OpReturn}
	for _, tc := range []struct {
		name  string
		code  []machine.Instruction
		want  []bool
		label int // index of the instruction label 0 names, or -1
	}{
		{"return", []machine.Instruction{add, sub, ret}, []bool{true, true, false}, -1},
		{"branch reads", []machine.Instruction{add, branch, ret}, []bool{false, false, false}, 2},
		{"overwritten", []machine.Instruction{add, compare, branch, ret}, []bool{true, false, false, false}, 3},
		// INC leaves CF alone, so a carry read behind it does not need it.
		{"carry passes inc", []machine.Instruction{compare, inc, carry, ret}, []bool{false, true, false, false}, -1},
		{"carry read", []machine.Instruction{sub, carry, ret}, []bool{false, false, false}, -1},
		// JO and JNO read the OF of the addition, CMOVO too.
		{"overflow branch", []machine.Instruction{add, overflow, ret}, []bool{false, false, false}, 2},
		{"no overflow branch", []machine.Instruction{sub, move, noOverflow, ret}, []bool{false, false, false, false}, 3},
		{"overflow select", []machine.Instruction{inc, selectOverflow, ret}, []bool{false, false, false}, -1},
		// The low word of a pair: its CF feeds ADC, or a flag word carry.
		{"pair carry", []machine.Instruction{add, high, ret}, []bool{false, false, false}, -1},
		{"pair carry after move", []machine.Instruction{sub, move, high, ret}, []bool{false, false, false, false}, -1},
		{"word carry", []machine.Instruction{add, wordCarry, ret}, []bool{false, false, false}, -1},
		{"opaque bytes", []machine.Instruction{add, {Op: machine.OpLiteralData, Data: []byte{0x90}}, ret}, []bool{false, false, false}, -1},
		{"same register", []machine.Instruction{{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAdd, Bits: 32, A: 7, B: 7, Imm: 8}, ret}, []bool{false, false}, -1},
		// The back edge carries the loop header's flag read to the add.
		{"back edge", []machine.Instruction{branch, add, {Op: machine.OpJump, Imm: 0}}, []bool{false, false, false}, 0},
		{"label after", []machine.Instruction{add, compare, branch, add, ret}, []bool{true, false, false, true, false}, 4},
		// A jump leaves the straight line; the whole graph finds the return.
		{"jump to return", []machine.Instruction{add, {Op: machine.OpJump, Imm: 0}, {Op: machine.OpLiteralData, Data: []byte{0x90}}, ret}, []bool{true, false, false, false}, 3},
		// The graph also finds an overflow read at a branch target.
		{"jump to overflow", []machine.Instruction{add, {Op: machine.OpJump, Imm: 0}, {Op: machine.OpLiteralData, Data: []byte{0x90}}, overflow, ret}, []bool{false, false, false, false, false}, 3},
		// Shorter only: LEA [ESP + 1] is as long as MOV and INC.
		{"not shorter", []machine.Instruction{{Op: machine.OpInc, A: 0, B: 4}, ret}, []bool{false, false}, -1},
		// A masked addition is LEA and MOVZX; other masked operations
		// keep their own flags but mask with MOVZX, which needs a byte
		// register at 8 bits.
		{"masked", []machine.Instruction{{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 16, A: 0, B: 7, C: 6}, ret}, []bool{true, false}, -1},
		{"masked xor", []machine.Instruction{{Op: machine.OpBinary, Arithmetic: op.OpXor, Bits: 8, A: 3, B: 3, C: 6}, ret}, []bool{true, false}, -1},
		{"masked xor without a byte register", []machine.Instruction{{Op: machine.OpBinary, Arithmetic: op.OpXor, Bits: 8, A: 6, B: 6, C: 1}, ret}, []bool{false, false}, -1},
		{"masked shift", []machine.Instruction{{Op: machine.OpShift, Arithmetic: op.OpShiftLeft, Bits: 16, A: 7, B: 7, Imm: 3}, ret}, []bool{true, false}, -1},
		// The narrow zero of the AND decides a branch: the AND stays.
		{"masked zero read", []machine.Instruction{{Op: machine.OpBinary, Arithmetic: op.OpSub, Bits: 8, A: 1, B: 1, C: 2}, branch, ret}, []bool{false, false, false}, 2},
		{"narrow zero read", []machine.Instruction{{Op: machine.OpNarrow, A: 0, B: 0, Imm: 16}, branch, ret}, []bool{false, false, false}, 2},
		{"narrow immediate", []machine.Instruction{{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAdd, Bits: 8, A: 0, B: 7, Imm: 3}, ret}, []bool{true, false}, -1},
		{"dec", []machine.Instruction{{Op: machine.OpDec, A: 0, B: 5}, ret}, []bool{true, false}, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := deadFlagStream(tc.code...)
			labels, bound := []int{0}, []bool{false}
			if tc.label >= 0 {
				pc := 0
				for _, i := range tc.code[:tc.label] {
					pc += s.Size(i)
				}
				labels[0], bound[0] = pc, true
			}
			moved, err := Policy{}.ElideDeadFlags(s, labels, bound)
			if err != nil {
				t.Fatal(err)
			}
			pc := 0
			for n, i := range s.Instructions() {
				if i.PreserveFlags != tc.want[n] {
					t.Errorf("instruction %d: PreserveFlags = %v, want %v", n, i.PreserveFlags, tc.want[n])
				}
				if n == tc.label && moved[0] != pc {
					t.Errorf("label at %d, want %d", moved[0], pc)
				}
				pc += s.SizeAt(n)
			}
			if pc != s.PC() {
				t.Errorf("stream PC %d, sizes sum to %d", s.PC(), pc)
			}
		})
	}
}

// OF has no stream flag: an instruction that writes some flags but not OF
// must not end an addition's OF before JO reads it, while INC and DEC,
// which write OF, do.
func TestElideDeadFlagsTracksOverflow(t *testing.T) {
	add := machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 32, A: 0, B: 7, C: 6}
	if killed := killedFlags(deadFlagStream(machine.Instruction{Op: machine.OpAtomic, Arithmetic: op.OpAtomicSwap, Bits: 64}), []machine.Instruction{{Op: machine.OpAtomic, Arithmetic: op.OpAtomicSwap, Bits: 64}}, 0); killed&flagOverflow != 0 {
		t.Errorf("CMPXCHG8B kills OF: %v", killed)
	}
	rol := machine.Instruction{Op: machine.OpShift, Arithmetic: op.OpRotateLeft, Bits: 32, Imm: 3, A: 2, B: 2}
	if killed := killedFlags(deadFlagStream(rol), []machine.Instruction{rol}, 0); killed&flagOverflow != 0 {
		t.Errorf("ROL kills OF: %v", killed)
	}
	inc := machine.Instruction{Op: machine.OpInc, A: 2, B: 2}
	if killed := killedFlags(deadFlagStream(inc), []machine.Instruction{inc}, 0); killed&flagOverflow == 0 {
		t.Errorf("INC keeps OF: %v", killed)
	}
	if reads := flagReads(&machine.Instruction{Op: machine.OpJumpIf, Condition: machine.BranchOverflow}); reads&flagOverflow == 0 {
		t.Errorf("JO reads %v", reads)
	}
	if c := candidateFlags(deadFlagStream(add), []machine.Instruction{add}, 0); c&flagOverflow == 0 {
		t.Errorf("ADD writes %v", c)
	}
}

func TestElideDeadFlagsRejectsUnboundTargets(t *testing.T) {
	s := deadFlagStream(
		machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 32, A: 0, B: 7, C: 6},
		machine.Instruction{Op: machine.OpJump, Imm: 0},
	)
	if _, err := (Policy{}).ElideDeadFlags(s, []int{0}, []bool{false}); err != machine.ErrInvalidBranch {
		t.Fatalf("unbound target: %v", err)
	}
}

func TestLEAEncodings(t *testing.T) {
	for _, tc := range []struct {
		ins  machine.Instruction
		want []byte
		text string
	}{
		{machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 32, A: 0, B: 1, C: 2}, []byte{0x8d, 0x04, 0x11}, "lea EAX, [ECX + EDX*1 + 0]"},
		{machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, A: 0, B: 5, C: 1}, []byte{0x8d, 0x04, 0x29}, "lea EAX, [ECX + EBP*1 + 0]"},
		{machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 32, A: 0, B: 5, C: 5}, []byte{0x8d, 0x44, 0x2d, 0x00}, "lea EAX, [EBP + EBP*1 + 0]"},
		{machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 32, A: 3, B: 6, C: 4}, []byte{0x8d, 0x1c, 0x34}, "lea EBX, [ESP + ESI*1 + 0]"},
		{machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAdd, Bits: 32, A: 0, B: 1, Imm: 7}, []byte{0x8d, 0x41, 0x07}, "lea EAX, [ECX + 7]"},
		{machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpSub, Bits: 16, A: 2, B: 4, Imm: 0x200}, []byte{0x8d, 0x94, 0x24, 0x00, 0xfe, 0xff, 0xff}, "lea EDX, [ESP + -512]"},
		{machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpSub, Bits: 32, A: 0, B: 1, Imm: 0x80000000}, []byte{0x8d, 0x81, 0x00, 0x00, 0x00, 0x80}, "lea EAX, [ECX + -2147483648]"},
		{machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAdd, Bits: 32, A: 0, B: 1, Imm: 0xffffffff}, []byte{0x8d, 0x41, 0xff}, "lea EAX, [ECX + -1]"},
		{machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAdd, Bits: 32, A: 0, B: 5}, []byte{0x8d, 0x45, 0x00}, "lea EAX, [EBP + 0]"},
		{machine.Instruction{Op: machine.OpDec, A: 1, B: 5}, []byte{0x8d, 0x4d, 0xff}, "lea ECX, [EBP + -1]"},
		{machine.Instruction{Op: machine.OpInc, A: 7, B: 3}, []byte{0x8d, 0x7b, 0x01}, "lea EDI, [EBX + 1]"},
	} {
		tc.ins.PreserveFlags = true
		a := NewByteAssembler(abi.StackPlatform)
		if err := machine.Visit(tc.ins, a); err != nil || a.Err() != nil {
			t.Fatalf("%+v: %v %v", tc.ins, err, a.Err())
		}
		if got := a.Bytes(); string(got) != string(tc.want) {
			t.Errorf("%+v: % x, want % x", tc.ins, got, tc.want)
		}
		if size := PhysicalSize(tc.ins, abi.StackPlatform, 0); size != len(tc.want) {
			t.Errorf("%+v: size %d, want %d", tc.ins, size, len(tc.want))
		}
		if f := FlagFacts(tc.ins); f.Modified != 0 || FlagEffect(tc.ins) {
			t.Errorf("%+v: LEA modifies flags %v", tc.ins, f.Modified)
		}
		text := NewTextAssembler(abi.StackPlatform)
		if err := machine.Visit(tc.ins, text); err != nil || text.Err() != nil {
			t.Fatalf("%+v: %v %v", tc.ins, err, text.Err())
		}
		if got := text.Text(); got != tc.text+"\n" || string(text.Bytes()) != string(tc.want) {
			t.Errorf("%+v: %q % x, want %q", tc.ins, got, text.Bytes(), tc.text)
		}
	}
	for _, ins := range []machine.Instruction{
		{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 32, A: 0, B: 4, C: 4},
		{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 8, A: 6, B: 1, C: 2},
		{Op: machine.OpBinary, Arithmetic: op.OpSub, Bits: 32, A: 0, B: 1, C: 2},
		{Op: machine.OpBinaryImmediate, Arithmetic: op.OpXor, Bits: 32, A: 0, B: 1, Imm: 1},
		{Op: machine.OpBinaryImmediate, Arithmetic: op.OpAdd, Bits: 32, A: 0, B: 1, Imm: 1 << 32},
		{Op: machine.OpInc, A: 8, B: 1},
	} {
		ins.PreserveFlags = true
		a := NewByteAssembler(abi.StackPlatform)
		if err := machine.Visit(ins, a); err == nil && a.Err() == nil {
			t.Errorf("%+v: assembled % x", ins, a.Bytes())
		}
		if size := PhysicalSize(ins, abi.StackPlatform, 0); size != 0 {
			t.Errorf("%+v: size %d", ins, size)
		}
	}
}

// The pass must not trust a stream's options for flag liveness: a size-only
// stream records no flag writes, and a legacy one records INC as writing CF.
func TestElideDeadFlagsIgnoresInexactStreams(t *testing.T) {
	size := func(i machine.Instruction) int { return PhysicalSize(i, abi.StackPlatform, 0) }
	streams := map[string]machine.StreamOptions{
		"size only": {Size: size},
		"legacy":    {Size: size, FlagEffect: FlagEffect},
		"exact":     {Size: size, FlagEffect: FlagEffect, FlagFacts: FlagFacts, ExactFlagFacts: true},
	}
	add := machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 32, A: 0, B: 7, C: 6}
	branch := machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchEqual, Imm: 0}
	inc := machine.Instruction{Op: machine.OpInc, A: 2, B: 2}
	carry := machine.Instruction{Op: machine.OpCarryBit, Bits: 32, Arithmetic: op.OpAdd, CarryMode: machine.CarryFlags, A: 3}
	ret := machine.Instruction{Op: machine.OpReturn}
	for name, options := range streams {
		for _, code := range [][]machine.Instruction{{add, branch, ret}, {add, inc, carry, ret}} {
			s := machine.NewStream(options)
			for _, i := range code {
				s.Append(i)
			}
			labels, bound := []int{0}, []bool{false}
			if code[1].Op == machine.OpJumpIf {
				labels[0], bound[0] = s.SizeAt(0)+s.SizeAt(1), true
			}
			if _, err := (Policy{}).ElideDeadFlags(s, labels, bound); err != nil {
				t.Fatal(err)
			}
			if s.Instructions()[0].PreserveFlags {
				t.Errorf("%s: add before %v became LEA although its flags are read", name, code[1].Op)
			}
		}
	}
}

// TestMayShortenKeepsCandidates: the cheap filter passes every instruction
// that has a shorter PreserveFlags form, over opcodes, operations, widths,
// registers, immediates and sizes.
func TestMayShortenKeepsCandidates(t *testing.T) {
	ariths := []op.Opcode{op.OpAdd, op.OpSub, op.OpAnd, op.OpOr, op.OpXor, op.OpMul,
		op.OpShiftLeft, op.OpShiftRight, op.OpShiftRightArithmetic, op.OpRotateLeft, op.OpRotateRight}
	checked, kept := 0, 0
	for code := machine.Opcode(0); code.Valid(); code++ {
		for _, arith := range ariths {
			for _, bits := range []uint8{0, 8, 16, 32, 64} {
				for _, imm := range []uint64{0, 1, 3, 0xff, 0x100, 0xffff, 0xffffffff, 1<<32 | 0xff} {
					for _, a := range []backend.Register{0, 3, 4, 5} {
						for _, b := range []backend.Register{0, 3, 4, 5} {
							for _, c := range []backend.Register{0, 5} {
								for _, preserve := range []bool{false, true} {
									i := machine.Instruction{Op: code, Arithmetic: arith, Bits: bits, Imm: imm, A: a, B: b, C: c, PreserveFlags: preserve}
									for _, size := range []int{2, 3, 4, 5, 6, 9} {
										checked++
										if _, ok := shorterWithoutFlags(&i, size); ok {
											kept++
											if !mayShorten(&i, size) {
												t.Fatalf("mayShorten rejects %+v at %d bytes", i, size)
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
	if kept == 0 {
		t.Fatalf("no candidate among %d", checked)
	}
}

// TestElideDeadFlagsReusesBuffers: a call after a larger one, whose
// buffers it may reuse, decides as it does alone.
func TestElideDeadFlagsReusesBuffers(t *testing.T) {
	add := machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 32, A: 0, B: 7, C: 6}
	compare := machine.Instruction{Op: machine.OpCompareRegister, Bits: 32, A: 7, B: 6}
	ret := machine.Instruction{Op: machine.OpReturn}
	// Each add's flags reach the loop header's branch through the back
	// edge, or the compare overwrites them.
	loop := func(n int, compares bool) []machine.Instruction {
		code := []machine.Instruction{{Op: machine.OpJumpIf, Condition: op.BranchEqual, Imm: 1}}
		for k := range n {
			code = append(code, add)
			if compares && k%2 == 0 {
				code = append(code, compare)
			}
		}
		return append(code, machine.Instruction{Op: machine.OpJump, Imm: 0}, ret)
	}
	elide := func(code []machine.Instruction) ([]bool, []int) {
		s := deadFlagStream(code...)
		labels, bound := []int{0, s.PC() - s.SizeAt(len(code)-1)}, []bool{true, true}
		moved, err := Policy{}.ElideDeadFlags(s, labels, bound)
		if err != nil {
			t.Fatal(err)
		}
		var preserved []bool
		for _, i := range s.Instructions() {
			preserved = append(preserved, i.PreserveFlags)
		}
		return preserved, moved
	}
	// The last addition before the back edge needs the whole graph.
	small := loop(2, true)
	wantPreserved, wantMoved := elide(small)
	for _, large := range [][]machine.Instruction{loop(200, true), loop(300, false)} {
		elide(large)
		preserved, moved := elide(small)
		if !slices.Equal(preserved, wantPreserved) || !slices.Equal(moved, wantMoved) {
			t.Fatalf("after a larger call: %v %v, alone %v %v", preserved, moved, wantPreserved, wantMoved)
		}
	}
	if want := []bool{false, true, false, false, false, false}; !slices.Equal(wantPreserved, want) {
		t.Fatalf("small loop decisions %v, want %v", wantPreserved, want)
	}
}
