//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"fmt"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

func compareWordArgument(width uint8, x uint64) any {
	if width == 64 {
		return x
	}
	if builder.NativeBits == 64 {
		return uintptr(x)
	}
	return uint32(x)
}
func TestCompareImmediateRegisterDifferential(t *testing.T) {
	immediates := []int64{0, 1, -1, 4095, 4096, -4095, -4096, -4097, 127, 128, -128, -129, 255, 256, 32767, 32768, -32768, -32769, 65535, 65536, 1<<31 - 1, 1 << 31, -1 << 31, -1<<31 - 1, 1<<32 - 1, 1 << 32, -1 << 32, 1<<63 - 1, -1 << 63}
	inputs := []uint64{0, 1, 2, 0x7f, 0x80, 0xff, 0x100, 0x7fff, 0x8000, 0xffff, 0x7fffffff, 0x80000000, 0xffffffff, 0x100000000, 0xfff, 0x1000, 0x1001, 1<<63 - 1, 1 << 63, ^uint64(0), ^uint64(0) - 4095, ^uint64(0) - 4096, 0xffffff80, 0xffffffffffffff80}
	cases, dirtyCases := 0, 0
	for _, width := range []uint8{64, 32, 16, 8} {
		for condition := ir.BranchEqual; condition <= ir.BranchAbove; condition++ {
			t.Run(fmt.Sprintf("%d/%d", width, condition), func(t *testing.T) {
				rb := n.NewBuilder()
				x, y := rb.ParamBits(width), rb.ParamBits(width)
				yes, no := rb.NewLabel(), rb.NewLabel()
				rb.Branch(rb.Compare(x, condition, y), yes)
				rb.Jump(no)
				rb.Bind(yes)
				rb.Return(rb.Const(1))
				rb.Bind(no)
				rb.Return(rb.Const(0))
				register := checkedIndexCode(t, rb, false)
				mask := ^uint64(0)
				if width < 64 {
					mask = uint64(1)<<width - 1
				}
				signed := condition >= ir.BranchLess && condition <= ir.BranchGreater
				for _, k := range immediates {
					b := n.NewBuilder()
					x := b.ParamBits(width)
					yes, no := b.NewLabel(), b.NewLabel()
					b.Branch(b.CompareImmediate(x, condition, k), yes)
					b.Jump(no)
					b.Bind(yes)
					b.Return(b.Const(1))
					b.Bind(no)
					b.Return(b.Const(0))
					valid := true
					if width < 64 {
						limit := int64(1) << (width - 1)
						if signed {
							valid = k >= -limit && k < limit
						} else {
							valid = k >= -limit && (k < 0 || uint64(k) <= mask)
						}
					}
					if !valid {
						if _, err := b.Finish(); err == nil {
							t.Fatalf("out-of-width immediate %d accepted", k)
						}
						continue
					}
					immediate := checkedIndexCode(t, b, false)
					ky := uint64(k) & mask
					for _, input := range inputs {
						xv := input & mask
						left, right := xv, ky
						if signed && width < 64 {
							left = uint64(int64(xv<<(64-width)) >> (64 - width))
							right = uint64(int64(ky<<(64-width)) >> (64 - width))
						}
						var want uint64
						if compareReference(condition, left, right) {
							want = 1
						}
						checkedIndexResult(t, immediate, want, compareWordArgument(width, xv))
						checkedIndexResult(t, register, want, compareWordArgument(width, xv), compareWordArgument(width, ky))
						if width < uint8(builder.NativeBits) {
							upper := ^mask
							if builder.NativeBits == 32 {
								upper &= 1<<32 - 1
							}
							// Supply dirty bits through the actual argument registers;
							// the Go reference alone masks them to the operand width.
							checkedIndexResult(t, immediate, want, compareWordArgument(width, xv|upper))
							for _, pair := range [][2]uint64{{xv | upper, ky}, {xv, ky | upper}, {xv | upper, ky | upper}} {
								checkedIndexResult(t, register, want, compareWordArgument(width, pair[0]), compareWordArgument(width, pair[1]))
							}
							dirtyCases++
						}
						cases++
					}
					immediate.Close()
				}
			})
		}
	}
	t.Logf("%d clean comparisons; %d dirty-upper-bit cases (immediate plus three register forms)", cases, dirtyCases)
	if dirtyCases == 0 {
		t.Fatal("no dirty upper bits exercised")
	}
	if cases != 18144 {
		t.Fatalf("coverage changed: %d want 18144", cases)
	}
}
