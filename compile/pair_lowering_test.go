package compile

import "testing"

func pairTestTarget(t *testing.T) machineTarget {
	t.Helper()
	return test386Target{abi: ABIC}
}

func pairTestEmitter() *physicalEmitter {
	return &physicalEmitter{
		size:       func(physicalInsn) int { return 1 },
		flagEffect: func(physicalInsn) bool { return false },
		frameBytes: func(slots int, _ bool) int { return slots * 4 },
		callBytes:  func(bool) int { return 0 },
		resultReg:  0,
	}
}

func pairTestValue(value uint64) machineValue {
	return machineValue{Words: 2, Constant: true, Imm: value, Param: -1, EntryReg: -1, EntrySlot: -1}
}

func executePairTestProgram(t *testing.T, program physicalProgram, incoming ...map[int]uint32) (uint32, uint32) {
	t.Helper()
	var regs [8]uint32
	spills := make(map[int]uint32)
	var carry uint32
	for _, located := range program.insns {
		ins := located.ins
		switch ins.Op {
		case pConst:
			regs[ins.A] = uint32(ins.Imm)
		case pMove:
			regs[ins.A] = regs[ins.B]
		case pBinary:
			if ins.Bits != 32 {
				t.Fatalf("word operation width = %d, want 32", ins.Bits)
			}
			switch ins.Arithmetic {
			case opAnd:
				regs[ins.A] = regs[ins.B] & regs[ins.C]
			case opOr:
				regs[ins.A] = regs[ins.B] | regs[ins.C]
			case opXor:
				regs[ins.A] = regs[ins.B] ^ regs[ins.C]
			case opAdd:
				sum := uint64(regs[ins.B]) + uint64(regs[ins.C])
				regs[ins.A], carry = uint32(sum), uint32(sum>>32)
			case opSub:
				left, right := regs[ins.B], regs[ins.C]
				if left < right {
					carry = 1
				} else {
					carry = 0
				}
				regs[ins.A] = left - right
			default:
				t.Fatalf("unexpected pair operation %d", ins.Arithmetic)
			}
		case pBinaryCarry:
			if ins.Bits != 32 {
				t.Fatalf("carry operation width = %d, want 32", ins.Bits)
			}
			switch ins.Arithmetic {
			case opAdd:
				sum := uint64(regs[ins.B]) + uint64(regs[ins.C]) + uint64(carry)
				regs[ins.A] = uint32(sum)
			case opSub:
				regs[ins.A] = regs[ins.B] - regs[ins.C] - carry
			default:
				t.Fatalf("unexpected carry operation %d", ins.Arithmetic)
			}
		case pSpill:
			spills[ins.Slot] = regs[ins.A]
		case pReload:
			regs[ins.A] = spills[ins.Slot]
		case pLoadIncoming:
			if len(incoming) == 0 {
				t.Fatal("unexpected incoming stack load")
			}
			regs[ins.A] = incoming[0][int(ins.Imm)]
		case pNarrow:
			switch ins.Imm {
			case 8:
				regs[ins.A] = regs[ins.B] & 0xff
			case 16:
				regs[ins.A] = regs[ins.B] & 0xffff
			case 32:
				regs[ins.A] = regs[ins.B]
			default:
				t.Fatalf("unexpected narrow width %d", ins.Imm)
			}
		case pReturn:
		default:
			t.Fatalf("unexpected physical operation %d", ins.Op)
		}
	}
	return regs[0], regs[2]
}

func TestPairIncomingStackArgument(t *testing.T) {
	target := pairTestTarget(t)
	program := machineProgram{
		Values: []machineValue{
			{Words: 2, Param: 0, EntryReg: -1, EntrySlot: -1},
			{Words: 2, Param: -1, EntryReg: -1, EntrySlot: -1},
		},
		Instructions: []machineInstruction{{Op: mMove, Dst: 1, A: 0}},
		Result:       1,
	}
	physical, _, _, err := allocateAndEmit(program, target, pairTestEmitter())
	if err != nil {
		t.Fatal(err)
	}
	loads := 0
	for _, located := range physical.insns {
		if located.ins.Op == pLoadIncoming {
			loads++
		}
	}
	if loads != 2 {
		t.Fatalf("incoming argument emitted %d stack loads, want two", loads)
	}
	low, high := executePairTestProgram(t, physical, map[int]uint32{4: 0x89abcdef, 8: 0x01234567})
	if got := uint64(high)<<32 | uint64(low); got != 0x0123456789abcdef {
		t.Fatalf("incoming argument = %#x", got)
	}
}

func TestPairAdditionOfStackArguments(t *testing.T) {
	target := pairTestTarget(t)
	program := machineProgram{
		Values: []machineValue{
			{Words: 2, Param: 0, EntryReg: -1, EntrySlot: -1},
			{Words: 2, Param: 1, EntryReg: -1, EntrySlot: -1},
			{Words: 2, Param: -1, EntryReg: -1, EntrySlot: -1},
		},
		Instructions: []machineInstruction{{Op: mBinary, Bits: 64, Arithmetic: opAdd, Dst: 2, A: 0, B: 1}},
		Result:       2,
	}
	physical, _, _, err := allocateAndEmit(program, target, pairTestEmitter())
	if err != nil {
		t.Fatal(err)
	}
	low, high := executePairTestProgram(t, physical, map[int]uint32{
		4: 0xffffffff, 8: 0x12345678,
		12: 2, 16: 0x9abcdef0,
	})
	if got := uint64(high)<<32 | uint64(low); got != 0xacf1356900000001 {
		t.Fatalf("stack argument sum = %#x", got)
	}
}

func TestPairArithmeticLowering(t *testing.T) {
	target := pairTestTarget(t)
	for _, tc := range []struct {
		op   op
		a, b uint64
		want uint64
	}{
		{opAdd, 0x12345678ffffffff, 0x9abcdef000000002, 0xacf1356900000001},
		{opSub, 0x1234567800000000, 0x0000000100000001, 0x12345676ffffffff},
	} {
		program := machineProgram{
			Values: []machineValue{pairTestValue(tc.a), pairTestValue(tc.b), {Words: 2, Param: -1, EntryReg: -1, EntrySlot: -1}},
			Instructions: []machineInstruction{
				{Op: mConst, Dst: 0}, {Op: mConst, Dst: 1},
				{Op: mBinary, Bits: 64, Arithmetic: tc.op, Dst: 2, A: 0, B: 1},
			},
			Result: 2,
		}
		physical, _, _, err := allocateAndEmit(program, target, pairTestEmitter())
		if err != nil {
			t.Fatalf("operation %d: %v", tc.op, err)
		}
		low, high := executePairTestProgram(t, physical)
		if got := uint64(high)<<32 | uint64(low); got != tc.want {
			t.Fatalf("operation %d result %#x, want %#x", tc.op, got, tc.want)
		}
		carryOps := 0
		for _, located := range physical.insns {
			if located.ins.Op == pBinaryCarry {
				carryOps++
			}
		}
		if carryOps != 1 {
			t.Fatalf("operation %d emitted %d high-word carry operations", tc.op, carryOps)
		}
	}
}

func TestPairBitwiseLowering(t *testing.T) {
	target := pairTestTarget(t)
	const a, b = uint64(0x12345678f0f0aa55), uint64(0xfedcba980f0f33cc)
	for _, tc := range []struct {
		op   op
		want uint64
	}{
		{opAnd, a & b}, {opOr, a | b}, {opXor, a ^ b},
	} {
		program := machineProgram{
			Values: []machineValue{pairTestValue(a), pairTestValue(b), {Words: 2, Param: -1, EntryReg: -1, EntrySlot: -1}},
			Instructions: []machineInstruction{
				{Op: mConst, Dst: 0}, {Op: mConst, Dst: 1},
				{Op: mBinary, Bits: 64, Arithmetic: tc.op, Dst: 2, A: 0, B: 1},
			},
			Result: 2,
		}
		physical, _, _, err := allocateAndEmit(program, target, pairTestEmitter())
		if err != nil {
			t.Fatalf("operation %d: %v", tc.op, err)
		}
		low, high := executePairTestProgram(t, physical)
		got := uint64(high)<<32 | uint64(low)
		if got != tc.want || physical.resultWordCount != 2 || physical.resultWords != [2]physReg{0, 2} {
			t.Fatalf("operation %d: result %#x, words %+v, count %d; want %#x", tc.op, got, physical.resultWords, physical.resultWordCount, tc.want)
		}
	}
}

func TestPairMoveKeepsLiveSource(t *testing.T) {
	target := pairTestTarget(t)
	const value = uint64(0xdeadbeef01234567)
	program := machineProgram{
		Values: []machineValue{
			pairTestValue(value), pairTestValue(0),
			{Words: 2, Param: -1, EntryReg: -1, EntrySlot: -1},
			{Words: 2, Param: -1, EntryReg: -1, EntrySlot: -1},
		},
		Instructions: []machineInstruction{
			{Op: mConst, Dst: 0}, {Op: mConst, Dst: 1},
			{Op: mMove, Dst: 2, A: 0},
			{Op: mBinary, Bits: 64, Arithmetic: opXor, Dst: 3, A: 0, B: 1},
		},
		Result: 2,
	}
	physical, _, _, err := allocateAndEmit(program, target, pairTestEmitter())
	if err != nil {
		t.Fatal(err)
	}
	moves := 0
	for _, located := range physical.insns {
		if located.ins.Op == pMove {
			moves++
		}
	}
	if moves < 2 {
		t.Fatalf("live pair copy emitted %d moves, want at least two", moves)
	}
	low, high := executePairTestProgram(t, physical)
	if got := uint64(high)<<32 | uint64(low); got != value {
		t.Fatalf("copied pair = %#x, want %#x", got, value)
	}
}

func TestNarrowPairResultZeroExtends(t *testing.T) {
	target := pairTestTarget(t)
	for _, tc := range []struct {
		width uint64
		want  uint32
	}{
		{8, 0xef}, {16, 0xcdef}, {32, 0x89abcdef},
	} {
		program := machineProgram{
			Values: []machineValue{pairTestValue(0xdeadbeef89abcdef), {Words: 1, Param: -1, EntryReg: -1, EntrySlot: -1}},
			Instructions: []machineInstruction{
				{Op: mConst, Dst: 0}, {Op: mNarrow, Dst: 1, A: 0, Imm: tc.width},
			},
			Result: 1,
		}
		physical, _, _, err := allocateAndEmit(program, target, pairTestEmitter())
		if err != nil {
			t.Fatal(err)
		}
		low, high := executePairTestProgram(t, physical)
		if low != tc.want || high != 0 || physical.resultWordCount != 2 {
			t.Fatalf("%d-bit narrow result = %#x:%#x, count %d, want %#x", tc.width, high, low, physical.resultWordCount, tc.want)
		}
	}
}

func TestPairResultSwapUsesTemporarySlot(t *testing.T) {
	emitter := pairTestEmitter()
	emitter.constant(2, 0x11223344)
	emitter.constant(0, 0x55667788)
	program, slots, err := emitter.finishPair(2, 0, 0, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if slots != 1 || len(program.insns) < 6 || program.insns[2].ins.Op != pSpill ||
		program.insns[3].ins.Op != pMove || program.insns[4].ins.Op != pReload {
		t.Fatalf("swap schedule = %+v, slots %d", program.insns, slots)
	}
	low, high := executePairTestProgram(t, program)
	if low != 0x11223344 || high != 0x55667788 {
		t.Fatalf("swapped result = %#x:%#x", high, low)
	}
}

func TestPairArithmeticDoesNotClaimScalarFlags(t *testing.T) {
	target := pairTestTarget(t)
	emitter := pairTestEmitter()
	emitter.flagEffect = func(ins physicalInsn) bool { return ins.Op == pBinary || ins.Op == pBinaryCarry }
	emitter.binarySetsFlags = true
	program := machineProgram{
		Values: []machineValue{pairTestValue(1), pairTestValue(2), {Words: 2, Param: -1, EntryReg: -1, EntrySlot: -1}},
		Instructions: []machineInstruction{
			{Op: mConst, Dst: 0}, {Op: mConst, Dst: 1},
			{Op: mBinary, Bits: 64, Arithmetic: opAdd, Dst: 2, A: 0, B: 1},
		},
		Result: 2,
	}
	physical, _, _, err := allocateBranchBlock(program, target, emitter)
	if err != nil {
		t.Fatal(err)
	}
	if physical.flags.Sign.Valid || physical.flags.Zero.Valid || physical.flags.Carry.Valid {
		t.Fatalf("high-word flags incorrectly describe full pair: %+v", physical.flags)
	}
}

func TestNarrowAliasKeepsLivePairIntact(t *testing.T) {
	const original = uint64(0x12345678deadbeef)
	for _, tc := range []struct {
		width uint64
	}{
		{8}, {16}, {32},
	} {
		t.Run(map[uint64]string{8: "8", 16: "16", 32: "32"}[tc.width], func(t *testing.T) {
			// Both words of the source pair remain live after narrowing.
			// The narrow destination must use separate physical storage.
			program := machineProgram{
				Values: []machineValue{
					pairTestValue(original), pairTestValue(0),
					{Words: 1, Param: -1, EntryReg: -1, EntrySlot: -1},
					{Words: 2, Param: -1, EntryReg: -1, EntrySlot: -1},
					{Words: 1, Param: -1, EntryReg: -1, EntrySlot: -1},
				},
				Instructions: []machineInstruction{
					{Op: mConst, Dst: 0}, {Op: mConst, Dst: 1},
					{Op: mNarrow, Dst: 2, A: 0, Imm: tc.width},
					{Op: mBinary, Bits: 64, Arithmetic: opXor, Dst: 3, A: 0, B: 1},
					{Op: mNarrow, Dst: 4, A: 3, Imm: tc.width},
				},
				Result: 3,
			}
			physical, _, _, err := allocateAndEmit(program, pairTestTarget(t), pairTestEmitter())
			if err != nil {
				t.Fatal(err)
			}
			foundFirstNarrow := false
			for _, located := range physical.insns {
				ins := located.ins
				if ins.Op != pNarrow {
					continue
				}
				foundFirstNarrow = true
				if ins.A == ins.B {
					t.Fatalf("narrow %d-bit view overwrote a live pair word in register %d", tc.width, ins.A)
				}
				break
			}
			if !foundFirstNarrow {
				t.Fatal("missing first narrow operation")
			}
			low, high := executePairTestProgram(t, physical)
			if got := uint64(high)<<32 | uint64(low); got != original {
				t.Fatalf("live pair after %d-bit alias = %#x, want %#x", tc.width, got, original)
			}
		})
	}
}

func TestPairSpillAndReloadUnderPressure(t *testing.T) {
	a, b, c, d := uint64(0xfeedfaceffffffff), uint64(0x1234567800000002),
		uint64(0xaaaaaaaa11111111), uint64(0x55555555eeeeeeee)
	program := machineProgram{
		Values: []machineValue{
			{Words: 2, Param: 0, EntryReg: -1, EntrySlot: -1},
			{Words: 2, Param: 1, EntryReg: -1, EntrySlot: -1},
			{Words: 2, Param: 2, EntryReg: -1, EntrySlot: -1},
			{Words: 2, Param: 3, EntryReg: -1, EntrySlot: -1},
			{Words: 2, Param: -1, EntryReg: -1, EntrySlot: -1},
			{Words: 2, Param: -1, EntryReg: -1, EntrySlot: -1},
			{Words: 2, Param: -1, EntryReg: -1, EntrySlot: -1},
			{Words: 2, Param: -1, EntryReg: -1, EntrySlot: -1},
		},
		Instructions: []machineInstruction{
			{Op: mBinary, Bits: 64, Arithmetic: opAdd, Dst: 4, A: 0, B: 1},
			{Op: mBinary, Bits: 64, Arithmetic: opAdd, Dst: 5, A: 2, B: 3},
			{Op: mBinary, Bits: 64, Arithmetic: opSub, Dst: 6, A: 4, B: 5},
			{Op: mBinary, Bits: 64, Arithmetic: opXor, Dst: 7, A: 0, B: 6},
		},
		Result: 7,
	}
	physical, _, _, err := allocateAndEmit(program, pairTestTarget(t), pairTestEmitter())
	if err != nil {
		t.Fatal(err)
	}
	spills, reloads := 0, 0
	for _, located := range physical.insns {
		switch located.ins.Op {
		case pSpill:
			spills++
		case pReload:
			reloads++
		}
	}
	if spills < 2 || reloads < 2 || spills%2 != 0 || reloads%2 != 0 {
		t.Fatalf("pair pressure emitted %d spills and %d reloads", spills, reloads)
	}
	low, high := executePairTestProgram(t, physical, map[int]uint32{
		4: uint32(a), 8: uint32(a >> 32), 12: uint32(b), 16: uint32(b >> 32),
		20: uint32(c), 24: uint32(c >> 32), 28: uint32(d), 32: uint32(d >> 32),
	})
	want := a ^ ((a + b) - (c + d))
	if got := uint64(high)<<32 | uint64(low); got != want {
		t.Fatalf("pair pressure result = %#x, want %#x", got, want)
	}
}

func TestPairSelfAdditionAliasesBothInputs(t *testing.T) {
	const value = uint64(0x7fffffff80000000)
	program := machineProgram{
		Values: []machineValue{
			pairTestValue(value),
			{Words: 2, Param: -1, EntryReg: -1, EntrySlot: -1},
		},
		Instructions: []machineInstruction{
			{Op: mConst, Dst: 0},
			{Op: mBinary, Bits: 64, Arithmetic: opAdd, Dst: 1, A: 0, B: 0},
		},
		Result: 1,
	}
	physical, _, _, err := allocateAndEmit(program, pairTestTarget(t), pairTestEmitter())
	if err != nil {
		t.Fatal(err)
	}
	low, high := executePairTestProgram(t, physical)
	if got, want := uint64(high)<<32|uint64(low), value+value; got != want {
		t.Fatalf("self-add pair = %#x, want %#x", got, want)
	}
}
