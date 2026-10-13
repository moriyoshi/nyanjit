package compile

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"math"
	"regexp"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

// A blinded share can coincidentally contain a source constant's 16-bit chunk.
// Force that case: the low 16 bits of x XOR this key equal x's low 16 bits.
type baselineBlindChunkRandom struct{}

func (baselineBlindChunkRandom) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0xa5
		if i%8 < 2 {
			p[i] = 0
		}
	}
	return len(p), nil
}

// Integer and floating-point constants, shift counts and scales are decoded
// from two shares and never loaded from the literal pool. Check the complete
// physical decode rather than matching random fragments in the listing.
func TestBaselineHardenBlindsConstants(t *testing.T) {
	previous := rand.Reader
	rand.Reader = baselineBlindChunkRandom{}
	t.Cleanup(func() { rand.Reader = previous })
	const k = 0x3141592653589793
	fp := math.Float64bits(1.0625)
	b := builder.NewBuilder()
	x, _ := b.Param()
	y, _ := b.Param()
	must := func(v builder.Value, err error) builder.Value {
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	c := must(b.Const(k))
	v := must(b.Xor(x, c))
	scale := must(b.Const(0x1234567))
	v = must(b.Add(v, must(b.Mul(y, scale))))
	v = must(b.Add(v, must(b.ShiftLeft(y, 13))))
	f := must(b.FFromSigned(v, 64))
	v = must(b.Xor(v, must(b.FMul(f, must(b.Float64(math.Float64frombits(fp)))))))
	if err := b.Return(v); err != nil {
		t.Fatal(err)
	}
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	little := func(x uint64, n int) []byte {
		var w [8]byte
		binary.LittleEndian.PutUint64(w[:], x)
		return w[:n]
	}
	for _, c := range []struct {
		arch     string
		patterns [][]byte
		listing  *regexp.Regexp
	}{
		{"amd64", [][]byte{little(k, 8), little(0x1234567, 4), little(fp, 8)}, regexp.MustCompile(`(?m)\bsh[lr] \w+, 13$`)},
		{"386", [][]byte{little(k, 4), little(k>>32, 4), little(0x1234567, 4), little(fp>>32, 4)}, regexp.MustCompile(`(?m)\bsh[lr]d? .*, 13$`)},
		{"arm64", [][]byte{little(fp, 8)}, regexp.MustCompile(`#0x9793\b|#0x4567\b|lsl #13$`)},
		{"riscv64", nil, regexp.MustCompile(`(?m)0x9793\b|slli .*, 13$`)},
		{"ppc64le", nil, regexp.MustCompile(`(?m)0x9793\b|0x4567\b|, 13, 50$`)},
		{"loong64", nil, regexp.MustCompile(`(?m)0x793\b|0x567\b|slli\.d .*, 13$`)},
	} {
		for _, harden := range []bool{false, true} {
			spec := TargetSpec{Architecture: c.arch, Tier: TierBaseline}
			u, err := lowerUnit(p, spec, Func{ABI: abi.C, Harden: harden})
			if err != nil {
				t.Fatalf("%s harden %v: %v", c.arch, harden, err)
			}
			listing, err := u.Listing()
			if err != nil {
				t.Fatal(err)
			}
			found := c.listing.MatchString(listing)
			for _, pattern := range c.patterns {
				found = found || bytes.Contains(u.baseline.code, pattern)
			}
			if harden {
				var decoded []uint64
				for n := 0; n < len(u.baseline.phys); n++ {
					key := u.baseline.phys[n]
					if key.k != pkConst {
						continue
					}
					if n+2 >= len(u.baseline.phys) {
						t.Fatalf("%s: constant without a decode: %+v", c.arch, key)
					}
					encoded, xor := u.baseline.phys[n+1], u.baseline.phys[n+2]
					if encoded.k != pkConst || xor.k != pkOp || ir.Opcode(xor.sub) != ir.OpXor || xor.a != key.a || xor.b != key.a || xor.c != encoded.a || key.a == encoded.a {
						t.Fatalf("%s: invalid constant decode: %+v", c.arch, u.baseline.phys[n:n+3])
					}
					decoded = append(decoded, key.imm^encoded.imm)
					n += 2
				}
				if want := []uint64{k, 0x1234567, 13, fp}; !slices.Equal(decoded, want) {
					t.Errorf("%s: decoded constants %x, want %x", c.arch, decoded, want)
				}
			}
			if !harden && !found {
				t.Errorf("%s: the fixture does not show its constants unhardened:\n%s", c.arch, listing)
			}
			if harden && len(u.baseline.pool) != 0 {
				t.Errorf("%s: hardened code has a literal pool", c.arch)
			}
		}
	}
}

// Hardened bounds checks clamp on their success edge's own path: the
// record after the success edge's barrier and moves, before its jump.
func TestBaselineHardenClampRecords(t *testing.T) {
	if builder.NativeBits != 64 {
		t.Skip("cross-target fixture uses 64-bit builder addresses")
	}
	b := builder.NewBuilder()
	base, _ := b.ParamNative()
	index, _ := b.ParamNative()
	count, _ := b.ParamNative()
	bad := b.NewLabel()
	address, err := b.IndexedAddress(base, index, count, 8, bad)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := b.Load64(address)
	_ = b.Return(value)
	_ = b.Bind(bad)
	zero, _ := b.Const(0)
	_ = b.Return(zero)
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"amd64", "386", "arm64", "ppc64le"} {
		u, err := lowerUnit(p, TargetSpec{Architecture: arch, Tier: TierBaseline}, Func{ABI: abi.C, Harden: true, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
		if err != nil {
			t.Fatal(err)
		}
		clamps, barriers, labels := 0, 0, 0
		var last uint8
		for _, ph := range u.baseline.phys {
			switch ph.k {
			case pkClamp:
				clamps++
				if barriers == 0 {
					t.Errorf("%s: clamp before any barrier", arch)
				}
			case pkBarrier:
				barriers++
				if last != pkLabel {
					t.Errorf("%s: barrier not at a label", arch)
				}
			case pkLabel:
				labels++
			case pkCmpImm:
				t.Errorf("%s: hardened comparison with an immediate", arch)
			}
			last = ph.k
		}
		// One clamp per word: 386 keeps the 64-bit index in a register pair.
		want := 1
		if arch == "386" {
			want = 2
		}
		if clamps != want || barriers != labels-1 {
			t.Errorf("%s: %d clamps, %d barriers for %d labels", arch, clamps, barriers, labels)
		}
	}
}
