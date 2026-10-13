package amd64_test

import (
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/amd64"
)

// sseMachine runs the SSE2 subset that shuffle and extend plans emit.
type sseMachine [16][16]byte

func (m *sseMachine) run(t *testing.T, code []byte) {
	t.Helper()
	for len(code) > 0 {
		prefix := byte(0)
		if code[0] == 0x66 || code[0] == 0xf2 || code[0] == 0xf3 {
			prefix, code = code[0], code[1:]
		}
		rex := byte(0)
		if true && code[0]&0xf0 == 0x40 {
			rex, code = code[0], code[1:]
		}
		if len(code) < 3 || code[0] != 0x0f || code[2]>>6 != 3 {
			t.Fatalf("cannot decode % x", code)
		}
		opcode, modrm := code[1], code[2]
		code = code[3:]
		d, s := modrm>>3&7|(rex>>2&1)<<3, modrm&7|(rex&1)<<3
		imm := byte(0)
		if opcode == 0x70 || opcode == 0xc6 {
			imm, code = code[0], code[1:]
		}
		dst, src := m[d], m[s]
		var out [16]byte
		word := func(v [16]byte, size, i int) uint64 {
			var b [8]byte
			copy(b[:], v[i*size:(i+1)*size])
			return binary.LittleEndian.Uint64(b[:])
		}
		put := func(size, i int, x uint64) {
			var b [8]byte
			binary.LittleEndian.PutUint64(b[:], x)
			copy(out[i*size:(i+1)*size], b[:size])
		}
		switch {
		case prefix == 0x66 && opcode == 0x6f:
			out = src
		case prefix == 0x66 && opcode == 0xef:
			for i := range out {
				out[i] = dst[i] ^ src[i]
			}
		case prefix == 0x66 && opcode == 0x70:
			for i := range 4 {
				put(4, i, word(src, 4, int(imm>>(2*i)&3)))
			}
		case prefix == 0xf2 && opcode == 0x70:
			out = src
			for i := range 4 {
				put(2, i, word(src, 2, int(imm>>(2*i)&3)))
			}
		case prefix == 0xf3 && opcode == 0x70:
			out = src
			for i := range 4 {
				put(2, 4+i, word(src, 2, 4+int(imm>>(2*i)&3)))
			}
		case prefix == 0 && opcode == 0xc6:
			put(4, 0, word(dst, 4, int(imm&3)))
			put(4, 1, word(dst, 4, int(imm>>2&3)))
			put(4, 2, word(src, 4, int(imm>>4&3)))
			put(4, 3, word(src, 4, int(imm>>6&3)))
		case prefix == 0x66 && opcode == 0xc6:
			put(8, 0, word(dst, 8, int(imm&1)))
			put(8, 1, word(src, 8, int(imm>>1&1)))
		case prefix == 0x66 && (opcode >= 0x60 && opcode <= 0x62 || opcode >= 0x68 && opcode <= 0x6a || opcode == 0x6c || opcode == 0x6d):
			size := 1 << (opcode & 3)
			if opcode >= 0x6c {
				size = 8
			}
			n := 16 / size
			base := 0
			if opcode >= 0x68 && opcode != 0x6c {
				base = n / 2
			}
			for k := 0; k < n/2; k++ {
				put(size, 2*k, word(dst, size, base+k))
				put(size, 2*k+1, word(src, size, base+k))
			}
		case prefix == 0x66 && opcode >= 0x64 && opcode <= 0x66:
			size := 1 << (opcode - 0x64)
			shift := 64 - 8*size
			for i := range 16 / size {
				if int64(word(dst, size, i)<<shift)>>shift > int64(word(src, size, i)<<shift)>>shift {
					put(size, i, ^uint64(0))
				}
			}
		default:
			t.Fatalf("unexpected instruction %02x 0f %02x", prefix, opcode)
		}
		m[d] = out
	}
}

func lanes(v [16]byte, bits int, n int) []uint64 {
	out := make([]uint64, n)
	for i := range out {
		var b [8]byte
		copy(b[:], v[i*bits/8:(i+1)*bits/8])
		out[i] = binary.LittleEndian.Uint64(b[:])
	}
	return out
}

func sseAssemble(ins machine.Instruction) ([]byte, string, error) {
	binary := amd64.NewByteAssembler(abi.StackPlatform, 0)
	if err := binary.Vector(ins); err != nil {
		return nil, "", err
	}
	text := amd64.NewTextAssembler(abi.StackPlatform, 0)
	if err := text.Vector(ins); err != nil {
		return nil, "", err
	}
	return binary.Bytes(), text.Text(), nil
}

// sseRegisters lists destination/source aliasings, including A == C.
var sseRegisters = [][3]uint8{{3, 4, 5}, {3, 3, 5}, {3, 4, 4}, {4, 4, 4}, {3, 4, 3}, {5, 4, 3}, {7, 6, 5}, {12, 4, 13}}

// checkShuffle assembles a shuffle for each register assignment and, when
// accepted, runs it on random registers and checks every lane, that only
// A and XMM0 change, and that VectorSize agrees. It returns acceptance.
func checkShuffle(t *testing.T, random *rand.Rand, bits int, idx []int) bool {
	t.Helper()
	n := 128 / bits
	var imm, disp uint64
	for i, x := range idx {
		if i < 8 {
			imm |= uint64(x) << (8 * i)
		} else {
			disp |= uint64(x) << (8 * (i - 8))
		}
	}
	accepted := -1
	for _, r := range sseRegisters {
		v := backend.VectorRegister
		ins := machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVShuffle, Bits: uint8(bits), A: v(r[0]), B: v(r[1]), C: v(r[2]), Imm: imm, Disp: disp}
		code, _, err := sseAssemble(ins)
		size := amd64.VectorSize(ins, abi.StackPlatform, 0)
		ok := err == nil
		if size != len(code) {
			t.Fatalf("bits %d %v regs %v: size %d, assembled %d bytes, err %v", bits, idx, r, size, len(code), err)
		}
		// Distinct sources decide; one source register (B == C) may only
		// make more patterns encodable.
		if r[1] != r[2] {
			if accepted >= 0 && ok != (accepted == 1) {
				t.Fatalf("bits %d %v: acceptance depends on registers %v", bits, idx, r)
			}
			accepted = 0
			if ok {
				accepted = 1
			}
		} else if !ok && accepted == 1 {
			t.Fatalf("bits %d %v: rejected for one source register %v", bits, idx, r)
		}
		if !ok {
			continue
		}
		var m sseMachine
		for i := range m {
			for j := range m[i] {
				m[i][j] = byte(random.Uint32())
			}
		}
		before := m
		m.run(t, code)
		x, y := lanes(before[r[1]], bits, n), lanes(before[r[2]], bits, n)
		got := lanes(m[r[0]], bits, n)
		for i, k := range idx {
			want := x[k%n]
			if k >= n {
				want = y[k-n]
			}
			if got[i] != want {
				t.Fatalf("bits %d %v regs %v: lane %d = %#x, want %#x\n% x", bits, idx, r, i, got[i], want, code)
			}
		}
		for reg := 1; reg < len(m); reg++ {
			if reg != int(r[0]) && m[reg] != before[reg] {
				t.Fatalf("bits %d %v regs %v: clobbered xmm%d", bits, idx, r, reg)
			}
		}
	}
	return accepted == 1
}

func TestShuffleEmulated(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 2))
	// Every 64- and 32-bit pattern is native.
	for bits, n := range map[int]int{64: 2, 32: 4} {
		total := 1
		for range n {
			total *= 2 * n
		}
		for code := range total {
			idx := make([]int, n)
			for i, c := 0, code; i < n; i, c = i+1, c/(2*n) {
				idx[i] = c % (2 * n)
			}
			if !checkShuffle(t, random, bits, idx) {
				t.Fatalf("bits %d %v rejected", bits, idx)
			}
		}
	}
	// Required byte and halfword patterns.
	for _, bits := range []int{8, 16} {
		n := 128 / bits
		pattern := func(f func(i int) int) []int {
			idx := make([]int, n)
			for i := range idx {
				idx[i] = f(i)
			}
			return idx
		}
		var required [][]int
		for lane := range 2 * n {
			required = append(required, pattern(func(int) int { return lane }))
		}
		for _, src := range [][2]int{{0, n}, {n, 0}, {0, 0}, {n, n}} {
			required = append(required,
				pattern(func(i int) int { return src[i%2] + i/2 }),
				pattern(func(i int) int { return src[i%2] + n/2 + i/2 }))
		}
		required = append(required, pattern(func(i int) int { return i }), pattern(func(i int) int { return n + i }))
		if bits == 16 {
			for _, idx := range [][]int{{7, 6, 5, 4, 3, 2, 1, 0}, {3, 2, 1, 0, 7, 6, 5, 4}, {1, 0, 3, 2, 5, 4, 7, 6}, {15, 14, 13, 12, 11, 10, 9, 8}, {4, 5, 6, 7, 0, 1, 2, 3}, {12, 13, 2, 3, 8, 9, 6, 7}} {
				required = append(required, idx)
			}
		}
		for _, idx := range required {
			if !checkShuffle(t, random, bits, idx) {
				t.Fatalf("bits %d %v rejected", bits, idx)
			}
		}
		accepted := 0
		for range 20000 {
			idx := make([]int, n)
			spread := []int{2 * n, n, 8, 4}[random.IntN(4)]
			base := random.IntN(2*n - spread + 1)
			for i := range idx {
				idx[i] = base + random.IntN(spread)
			}
			if random.IntN(3) == 0 { // aligned pairs
				for i := 0; i < n; i += 2 {
					idx[i+1] = idx[i] ^ 1
					idx[i] &^= 1
					idx[i+1] = idx[i] + 1
				}
			}
			if checkShuffle(t, random, bits, idx) {
				accepted++
			}
		}
		t.Logf("bits %d: %d of 20000 random patterns native", bits, accepted)
	}
	// A general byte reversal needs PSHUFB.
	if checkShuffle(t, random, 8, []int{15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0}) {
		t.Fatal("byte reversal accepted")
	}
}

func TestShuffleRejectsBadIndices(t *testing.T) {
	v := backend.VectorRegister
	for _, ins := range []machine.Instruction{
		{Bits: 32, Imm: 0x08},         // index 8 of 8 lanes
		{Bits: 32, Imm: 0x0100000000}, // byte past the lanes
		{Bits: 16, Imm: 0, Disp: 1},   // Disp past the lanes
		{Bits: 8, Imm: 0x20},          // index 32
		{Bits: 0, Imm: 0},             // no lane width
	} {
		ins.Op, ins.Arithmetic, ins.A, ins.B, ins.C = machine.OpVector, op.OpVShuffle, v(3), v(4), v(5)
		if _, _, err := sseAssemble(ins); err == nil || amd64.VectorSize(ins, abi.StackPlatform, 0) != 0 {
			t.Errorf("%+v accepted", ins)
		}
	}
}

func TestExtendEmulated(t *testing.T) {
	random := rand.New(rand.NewPCG(3, 4))
	v := backend.VectorRegister
	for _, operation := range []op.Opcode{op.OpVExtendUnsigned, op.OpVExtendSigned} {
		for _, bits := range []int{16, 32, 64} {
			for half := range uint64(2) {
				for _, r := range sseRegisters {
					ins := machine.Instruction{Op: machine.OpVector, Arithmetic: operation, Bits: uint8(bits), A: v(r[0]), B: v(r[1]), Imm: half}
					code, text, err := sseAssemble(ins)
					if err != nil || amd64.VectorSize(ins, abi.StackPlatform, 0) != len(code) {
						t.Fatalf("%v %d %d: %v, size %d of %d", operation, bits, half, err, amd64.VectorSize(ins, abi.StackPlatform, 0), len(code))
					}
					if half == 1 && !strings.Contains(text, "punpckh") || half == 0 && !strings.Contains(text, "punpckl") {
						t.Fatalf("listing %q", text)
					}
					var m sseMachine
					for i := range m {
						for j := range m[i] {
							m[i][j] = byte(random.Uint32())
						}
					}
					before := m
					m.run(t, code)
					n := 128 / bits
					in := lanes(before[r[1]], bits/2, 2*n)[int(half)*n:]
					got := lanes(m[r[0]], bits, n)
					for i := range n {
						want := in[i]
						if operation == op.OpVExtendSigned && want>>(bits/2-1)&1 != 0 {
							want |= ^uint64(0) << (bits / 2)
							if bits < 64 {
								want &= 1<<bits - 1
							}
						}
						if got[i] != want {
							t.Fatalf("%v %d %d regs %v: lane %d = %#x, want %#x", operation, bits, half, r, i, got[i], want)
						}
					}
					for reg := 1; reg < len(m); reg++ {
						if reg != int(r[0]) && m[reg] != before[reg] {
							t.Fatalf("clobbered xmm%d", reg)
						}
					}
				}
			}
		}
	}
	for _, ins := range []machine.Instruction{{Bits: 8}, {Bits: 16, Imm: 2}, {Bits: 128}} {
		ins.Op, ins.Arithmetic, ins.A, ins.B = machine.OpVector, op.OpVExtendSigned, v(3), v(4)
		if _, _, err := sseAssemble(ins); err == nil || amd64.VectorSize(ins, abi.StackPlatform, 0) != 0 {
			t.Errorf("%+v accepted", ins)
		}
	}
}

func TestShuffleListing(t *testing.T) {
	v := backend.VectorRegister
	for _, test := range []struct {
		ins        machine.Instruction
		hex, lines string
	}{
		{machine.Instruction{Bits: 32, Imm: 0x00010203}, "660f70dc1b", "pshufd xmm3, xmm4, 0x1b"},
		{machine.Instruction{Bits: 32, Imm: 0x06040301}, "660f6fdc0fc6dd8d", "movdqa xmm3, xmm4\nshufps xmm3, xmm5, 0x8d"},
		{machine.Instruction{Bits: 64, Imm: 0x0300}, "660f6fdc660fc6dd02", "movdqa xmm3, xmm4\nshufpd xmm3, xmm5, 0x2"},
		{machine.Instruction{Bits: 16, Imm: 0x0b030a0209010800}, "660f6fdc660f61dd", "movdqa xmm3, xmm4\npunpcklwd xmm3, xmm5"},
		{machine.Instruction{Bits: 8, Imm: 0x0505050505050505, Disp: 0x0505050505050505}, "660f6fdc660f60dcf30f70db55660f70dbaa", "punpcklbw xmm3, xmm4\npshufhw xmm3, xmm3, 0x55\npshufd xmm3, xmm3, 0xaa"},
		{machine.Instruction{Bits: 32, Imm: 0x03020100}, "660f6fdc", "movdqa xmm3, xmm4"},
	} {
		ins := test.ins
		ins.Op, ins.Arithmetic, ins.A, ins.B, ins.C = machine.OpVector, op.OpVShuffle, v(3), v(4), v(5)
		code, text, err := sseAssemble(ins)
		listed := true
		for _, line := range strings.Split(test.lines, "\n") {
			listed = listed && strings.Contains(text, line+"\n")
		}
		if err != nil || fmt.Sprintf("%x", code) != test.hex || !listed {
			t.Errorf("%+v: %x %q %v, want %s %q", test.ins, code, text, err, test.hex, test.lines)
		}
	}
}
