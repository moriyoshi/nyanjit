package compile_test

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func floatBranchTruth(c ir.FloatCondition, x, y float64) bool {
	unordered := math.IsNaN(x) || math.IsNaN(y)
	switch c {
	case ir.FloatEqual:
		return x == y
	case ir.FloatNotEqual:
		return x != y
	case ir.FloatLess:
		return x < y
	case ir.FloatUnorderedGreaterEqual:
		return !(x < y)
	case ir.FloatLessEqual:
		return x <= y
	case ir.FloatUnorderedGreater:
		return !(x <= y)
	case ir.FloatGreater:
		return x > y
	case ir.FloatUnorderedLessEqual:
		return !(x > y)
	case ir.FloatGreaterEqual:
		return x >= y
	case ir.FloatUnorderedLess:
		return !(x >= y)
	case ir.FloatOrdered:
		return !unordered
	case ir.FloatUnordered:
		return unordered
	case ir.FloatOrderedNotEqual:
		return !unordered && x != y
	case ir.FloatUnorderedEqual:
		return unordered || x == y
	}
	panic("invalid condition")
}

func TestARM64FloatBranchFusion(t *testing.T) { testFloatBranchFusion(t, "arm64") }
func TestAMD64FloatBranchFusion(t *testing.T) { testFloatBranchFusion(t, "amd64") }

func testFloatBranchFusion(t *testing.T, arch string) {
	for _, w := range []uint8{32, 64} {
		for c := ir.FloatEqual; c <= ir.FloatUnorderedEqual; c++ {
			for _, negated := range []bool{false, true} {
				for _, harden := range []bool{false, true} {
					t.Run(fmt.Sprintf("%d/%d/not=%t/harden=%t", w, c, negated, harden), func(t *testing.T) {
						b := n.NewBuilder()
						x, y := b.ParamBits(w), b.ParamBits(w)
						p := b.FComparePredicate(x, c, y)
						if negated {
							p = p.Not()
						}
						yes := b.NewLabel()
						b.Branch(p, yes)
						b.Return(b.Const(0))
						b.Bind(yes)
						b.Return(b.Const(1))
						source, err := b.Finish()
						if err != nil {
							t.Fatal(err)
						}
						target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch})
						if err != nil {
							t.Fatal(err)
						}
						u, err := compile.Lower(target, source, compile.Func{ABI: abi.C, Harden: harden})
						if err != nil {
							t.Fatal(err)
						}
						listing, err := u.Listing()
						if err != nil {
							t.Fatal(err)
						}
						eligible := c <= ir.FloatUnorderedLess
						if arch == "amd64" {
							eligible = c >= ir.FloatLess && c <= ir.FloatUnorderedLess || c >= ir.FloatOrderedNotEqual
						}
						eligible = eligible && !harden && os.Getenv("NYANJIT_INTERNAL_ONLINE_ALLOCATION") != "1"
						if arch == "arm64" && eligible && (strings.Contains(listing, "cset ") || strings.Contains(listing, "cbnz ") || strings.Contains(listing, "cbz ")) {
							t.Fatalf("boolean materialization remains:\n%s", listing)
						}
						if arch == "arm64" && !eligible && !strings.Contains(listing, "cset ") {
							t.Fatalf("fallback lost its boolean:\n%s", listing)
						}
						if arch == "amd64" && eligible && (strings.Contains(listing, "cmp ") || strings.Contains(listing, "test ")) {
							t.Fatalf("boolean retest remains:\n%s", listing)
						}
						if runtime.GOOS != "linux" || runtime.GOARCH != arch {
							return
						}
						tmpl, err := u.Template()
						if err != nil {
							t.Fatal(err)
						}
						code, err := tmpl.Bind(nil)
						if err != nil {
							t.Fatal(err)
						}
						defer code.Close()
						values := []uint64{0, 1 << 63, math.Float64bits(-1), math.Float64bits(1), math.Float64bits(math.Inf(-1)), math.Float64bits(math.Inf(1)), 0x7ff8000000000123, 0x7ff0000000000001}
						if w == 32 {
							values = []uint64{0, 1 << 31, uint64(math.Float32bits(-1)), uint64(math.Float32bits(1)), 0xff800000, 0x7f800000, 0x7fc00123, 0x7f800001}
						}
						for _, a := range values {
							for _, z := range values {
								af, zf := math.Float64frombits(a), math.Float64frombits(z)
								if w == 32 {
									af, zf = float64(math.Float32frombits(uint32(a))), float64(math.Float32frombits(uint32(z)))
								}
								truth := floatBranchTruth(c, af, zf)
								if negated {
									truth = !truth
								}
								want := uintptr(0)
								if truth {
									want = 1
								}
								got, err := code.Call(uintptr(a), uintptr(z))
								if err != nil || got != want {
									t.Fatalf("f(%#x,%#x)=%d,%v; want %d\n%s", a, z, got, err, want, listing)
								}
							}
						}
					})
				}
			}
		}
	}
}

func TestARM64FloatBranchKeepsExportedBoolean(t *testing.T) {
	b := n.NewBuilder()
	x, y := b.Param(), b.Param()
	value := b.FCompare(x, ir.FloatLess, y)
	yes := b.NewLabel()
	b.Branch(b.IsZero(value).Not(), yes)
	b.Return(value)
	b.Bind(yes)
	b.Return(value)
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	listing, err := lowerListing(p, compile.TargetSpec{Architecture: "arm64"}, compile.Func{ABI: abi.C})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listing, "cset ") {
		t.Fatalf("exported boolean disappeared:\n%s", listing)
	}
}
