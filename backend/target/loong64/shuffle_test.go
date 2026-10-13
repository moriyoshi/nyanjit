package loong64_test

import (
	"encoding/binary"
	"math/rand/v2"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
)

// shuffleForm packs lane indices as the OpVShuffle form takes them.
func shuffleForm(bits uint8, a, b, c uint8, indices []int) machine.Instruction {
	f := backend.VectorRegister
	i := machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVShuffle, Bits: bits, A: f(a), B: f(b), C: f(c)}
	for k, x := range indices {
		if k < 8 {
			i.Imm |= uint64(x) << (8 * k)
		} else {
			i.Disp |= uint64(x) << (8 * (k - 8))
		}
	}
	return i
}

// lsxModel runs the few LSX and GPR instructions the shuffle and extend
// expansions use, on 16-byte vector registers.
type lsxModel struct {
	v [32][16]byte
	r [32]uint64
}

func (m *lsxModel) lane(r uint32, size uint32, k int) uint64 {
	w := 1 << size
	var x [8]byte
	copy(x[:], m.v[r][k*w:(k+1)*w])
	return binary.LittleEndian.Uint64(x[:])
}

func (m *lsxModel) run(t *testing.T, words []uint32) {
	t.Helper()
	for _, w := range words {
		d, j, k, a := w&31, w>>5&31, w>>10&31, w>>15&31
		src := m.v
		set := func(size uint32, lanes func(n int) uint64) {
			w := 1 << size
			for n := 0; n < 16/w; n++ {
				var x [8]byte
				binary.LittleEndian.PutUint64(x[:], lanes(n))
				copy(m.v[d][n*w:], x[:w])
			}
		}
		old := lsxModel{v: src}
		switch {
		case w&0xffff8000 == 0x71268000: // vor.v
			for n := range 16 {
				m.v[d][n] = src[j][n] | src[k][n]
			}
		case w&0xffffc000 == 0x72f78000, w&0xffffe000 == 0x72f7c000, w&0xfffff000 == 0x72f7e000, w&0xfffff800 == 0x72f7f000:
			size := uint32(0)
			for w&(0x4000>>size) != 0 && size < 3 {
				size++
			}
			lane := int(w >> 10 & (0xf >> size))
			set(size, func(int) uint64 { return old.lane(j, size, lane) })
		case w&0xfffe0000 == 0x711a0000, w&0xfffe0000 == 0x711c0000, w&0xfffe0000 == 0x711e0000, w&0xfffe0000 == 0x71200000:
			size := w >> 15 & 3
			n := 16 >> size
			kind := w & 0xfffe0000
			set(size, func(e int) uint64 {
				switch kind {
				case 0x711a0000:
					if e%2 == 0 {
						return old.lane(k, size, e/2)
					}
					return old.lane(j, size, e/2)
				case 0x711c0000:
					if e%2 == 0 {
						return old.lane(k, size, n/2+e/2)
					}
					return old.lane(j, size, n/2+e/2)
				case 0x711e0000:
					if e < n/2 {
						return old.lane(k, size, 2*e)
					}
					return old.lane(j, size, 2*(e-n/2))
				default:
					if e < n/2 {
						return old.lane(k, size, 2*e+1)
					}
					return old.lane(j, size, 2*(e-n/2)+1)
				}
			})
		case w&0xfffc0000 == 0x739c0000: // vshuf4i.d: selector bit 1 picks vj over vd
			selector := int(w >> 10 & 0xff)
			set(3, func(e int) uint64 {
				x := selector >> (2 * e) & 3
				if x >= 2 {
					return old.lane(j, 3, x-2)
				}
				return old.lane(d, 3, x)
			})
		case w&0xfff00000 == 0x73900000:
			size := w >> 18 & 3
			selector := int(w >> 10 & 0xff)
			set(size, func(e int) uint64 { return old.lane(j, size, e&^3+selector>>(2*(e&3))&3) })
		case w&0xfff00000 == 0x0d500000:
			for n := range 16 {
				x := src[a][n] & 31
				if x < 16 {
					m.v[d][n] = src[k][x]
				} else {
					m.v[d][n] = src[j][x-16]
				}
			}
		case w&0xfffff800 == 0x72ebf000:
			binary.LittleEndian.PutUint64(m.v[d][8*(w>>10&1):], m.r[j])
		case w&0xfffffc00 == 0x729f0c00:
			binary.LittleEndian.PutUint64(m.v[d][:], m.r[j])
			binary.LittleEndian.PutUint64(m.v[d][8:], m.r[j])
		case w&0xffc00000 == 0x02c00000: // addi.d
			m.r[d] = m.r[j] + uint64(int64(int32(w<<10))>>20)
		case w&0xffc00000 == 0x03800000: // ori
			m.r[d] = m.r[j] | uint64(w>>10&4095)
		case w&0xfe000000 == 0x14000000: // lu12i.w
			m.r[d] = uint64(int64(int32(w>>5&0xfffff) << 12))
		case w&0xfe000000 == 0x16000000: // lu32i.d
			m.r[d] = m.r[d]&0xffffffff | uint64(int64(int32(w<<7))>>12)<<32
		case w&0xffc00000 == 0x03000000: // lu52i.d
			m.r[d] = m.r[j]&(1<<52-1) | uint64(w>>10&4095)<<52
		default:
			t.Fatalf("model: unknown word %08x", w)
		}
	}
}

func lsxWords(t *testing.T, i machine.Instruction) []uint32 {
	t.Helper()
	b := loong64.NewByteAssembler(abi.StackNoUse, 0)
	if err := machine.Visit(i, b); err != nil {
		t.Fatalf("%+v: %v", i, err)
	}
	if size := loong64.PhysicalSize(i, abi.StackPlatform, 0); size != len(b.Bytes()) {
		t.Fatalf("%+v: PhysicalSize %d, assembled %d", i, size, len(b.Bytes()))
	}
	text := loong64.NewTextAssembler(abi.StackNoUse, 0)
	if err := machine.Visit(i, text); err != nil || text.AssembledSize() != len(b.Bytes()) {
		t.Fatalf("%+v: text %v", i, err)
	}
	words := make([]uint32, len(b.Bytes())/4)
	for n := range words {
		words[n] = binary.LittleEndian.Uint32(b.Bytes()[4*n:])
	}
	return words
}

// TestLSXShuffleModel runs every expansion of random and patterned index
// sets in a model of the instructions used and compares the lanes.
func TestLSXShuffleModel(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 2))
	for _, bits := range []uint8{8, 16, 32, 64} {
		n := 128 / int(bits)
		w := int(bits / 8)
		sets := [][]int{}
		add := func(f func(k int) int) {
			s := make([]int, n)
			for k := range s {
				s[k] = f(k)
			}
			sets = append(sets, s)
		}
		add(func(k int) int { return k })
		add(func(k int) int { return n + k })
		add(func(k int) int { return n - 1 - k })
		add(func(k int) int { return k/2 + k%2*n })
		add(func(k int) int { return n/2 + k/2 + (1-k%2)*n })
		add(func(k int) int { return 2 * k })
		add(func(k int) int { return 2*k + 1 })
		add(func(k int) int { return k ^ 1 })
		add(func(k int) int { return 3 })
		add(func(k int) int { return n + 1 })
		for range 40 {
			add(func(int) int { return random.IntN(2 * n) })
			add(func(int) int { return random.IntN(n) })
		}
		for _, indices := range sets {
			for _, regs := range [][3]uint8{{3, 4, 5}, {4, 4, 5}, {5, 4, 5}, {4, 4, 4}, {6, 7, 7}, {31, 30, 29}} {
				i := shuffleForm(bits, regs[0], regs[1], regs[2], indices)
				words := lsxWords(t, i)
				var m lsxModel
				for r := range m.v {
					for n := range m.v[r] {
						m.v[r][n] = byte(r*16 + n)
					}
				}
				input := m.v
				m.run(t, words)
				for k, x := range indices {
					source := input[regs[1]]
					if x >= n {
						source = input[regs[2]]
					}
					if regs[1] == regs[2] {
						source = input[regs[1]]
					}
					want := source[(x%n)*w : (x%n+1)*w]
					if got := m.v[regs[0]][k*w : (k+1)*w]; string(got) != string(want) {
						t.Fatalf("bits %d regs %v indices %v: lane %d = %x, want %x (words %08x)", bits, regs, indices, k, got, want, words)
					}
				}
				for _, r := range loong64.VectorWrites(i) {
					if n := backend.VectorNumber(r); n != regs[0] && n != 0 {
						t.Fatalf("writes vr%d", n)
					}
				}
			}
		}
	}
}

func TestLSXShuffleEncodings(t *testing.T) {
	// Fixed bases agree with cmd/asm/internal/asm/testdata/loong64enc1.s
	// (VILVLB/H/W/V, VILVHB/V, VSHUF4IB/H/W, VSHUFB and the VMOVQ lane
	// broadcasts that encode vreplvei).
	for _, tc := range []struct {
		bits    uint8
		indices []int
		want    []uint32
	}{
		{8, []int{0, 16, 1, 17, 2, 18, 3, 19, 4, 20, 5, 21, 6, 22, 7, 23}, []uint32{0x711a0000 | 5<<5 | 4<<10 | 3}},
		{16, []int{0, 8, 1, 9, 2, 10, 3, 11}, []uint32{0x711a8000 | 5<<5 | 4<<10 | 3}},
		{32, []int{2, 6, 3, 7}, []uint32{0x711c0000 | 2<<15 | 5<<5 | 4<<10 | 3}},
		{64, []int{0, 2}, []uint32{0x711b8000 | 5<<5 | 4<<10 | 3}},
		{8, []int{3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3}, []uint32{0x72f78000 | 3<<10 | 4<<5 | 3}},
		{16, []int{10, 10, 10, 10, 10, 10, 10, 10}, []uint32{0x72f7c000 | 2<<10 | 5<<5 | 3}},
		{32, []int{1, 1, 1, 1}, []uint32{0x72f7e000 | 1<<10 | 4<<5 | 3}},
		{64, []int{3, 3}, []uint32{0x72f7f000 | 1<<10 | 5<<5 | 3}},
		{32, []int{3, 2, 1, 0}, []uint32{0x73980000 | 0x1b<<10 | 4<<5 | 3}},
		{8, []int{1, 0, 3, 2, 5, 4, 7, 6, 9, 8, 11, 10, 13, 12, 15, 14}, []uint32{0x73900000 | 0xb1<<10 | 4<<5 | 3}},
		{32, []int{0, 1, 2, 3}, []uint32{0x71268000 | 4<<10 | 4<<5 | 3}},
	} {
		words := lsxWords(t, shuffleForm(tc.bits, 3, 4, 5, tc.indices))
		if len(words) != len(tc.want) || words[0] != tc.want[0] {
			t.Errorf("bits %d indices %v: %08x, want %08x", tc.bits, tc.indices, words, tc.want)
		}
	}
	// A general byte shuffle builds its table in vr0 and ends with vshuf.b.
	reverse := make([]int, 16)
	for k := range reverse {
		reverse[k] = 31 - k
	}
	words := lsxWords(t, shuffleForm(8, 3, 4, 5, reverse))
	if last := words[len(words)-1]; last != 0x0d500000|4<<10|5<<5|3 {
		t.Errorf("vshuf.b = %08x", last)
	}
	// Extends (binutils loongarch-opc.c: vsllwil.{h.b,w.h,d.w}[u], vexth.*).
	for k, bits := range []uint8{16, 32, 64} {
		for _, arithmetic := range []op.Opcode{op.OpVExtendSigned, op.OpVExtendUnsigned} {
			for half := range uint64(2) {
				i := machine.Instruction{Op: machine.OpVector, Arithmetic: arithmetic, Bits: bits, A: backend.VectorRegister(3), B: backend.VectorRegister(4), Imm: half}
				want := []uint32{0x73082000, 0x73084000, 0x73088000}[k]
				if arithmetic == op.OpVExtendUnsigned {
					want += 0x40000
				}
				if half == 1 {
					want = []uint32{0x729ee000, 0x729ee400, 0x729ee800}[k]
					if arithmetic == op.OpVExtendUnsigned {
						want += 0x1000
					}
				}
				if words := lsxWords(t, i); len(words) != 1 || words[0] != want|4<<5|3 {
					t.Errorf("%v bits %d half %d: %08x, want %08x", arithmetic, bits, half, words, want|4<<5|3)
				}
			}
		}
	}
	f := backend.VectorRegister
	for _, i := range []machine.Instruction{
		shuffleForm(32, 3, 4, 5, []int{8, 0, 0, 0}),
		{Op: machine.OpVector, Arithmetic: op.OpVShuffle, Bits: 32, A: f(3), B: f(4), C: f(5), Imm: 1 << 32},
		{Op: machine.OpVector, Arithmetic: op.OpVShuffle, Bits: 64, A: f(3), B: f(4), C: f(5), Disp: 1},
		{Op: machine.OpVector, Arithmetic: op.OpVExtendSigned, Bits: 8, A: f(3), B: f(4)},
		{Op: machine.OpVector, Arithmetic: op.OpVExtendUnsigned, Bits: 32, A: f(3), B: f(4), Imm: 2},
	} {
		if err := machine.Visit(i, loong64.NewByteAssembler(abi.StackNoUse, 0)); err == nil || loong64.PhysicalSize(i, abi.StackPlatform, 0) != 0 {
			t.Errorf("%+v: accepted", i)
		}
	}
}
