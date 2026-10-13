//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"errors"
	"math"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// floatTiers names the tiers the float-passing tests run at.
var floatTiers = []struct {
	name string
	tier compile.Tier
}{{"optimized", compile.TierOptimized}, {"baseline", compile.TierBaseline}}

func floatFunc(flavor abi.Flavor, context compile.Context, floats abi.FloatPassing) compile.Func {
	return compile.Func{ABI: flavor, Context: context, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1024},
		AssumePointerFree: context == compile.ContextGoManaged, Floats: floats}
}

func floatCode(t *testing.T, tier compile.Tier, f compile.Func, build func(*nyanjit.Builder)) *execution.Code {
	t.Helper()
	target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
	if err != nil {
		t.Fatal(err)
	}
	b := nyanjit.NewBuilderFor(target, f)
	build(b)
	u, err := b.Lower()
	if err != nil {
		t.Fatalf("context %d, ABI %d, floats %#x: %v", f.Context, f.ABI, uint64(f.Floats), err)
	}
	if got := u.Report().Floats(); got != f.Floats {
		t.Fatalf("report float passing %#x, want %#x", uint64(got), uint64(f.Floats))
	}
	template, err := u.Template()
	if err != nil {
		t.Fatal(err)
	}
	code, err := template.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { code.Close() })
	return code
}

// callBits calls code with logical arguments (uint64 or uint32, which take
// one 386 word) and joins a 64-bit result on 386.
func callBits(t *testing.T, code *execution.Code, args ...any) uint64 {
	t.Helper()
	words := execution.Words(args...)
	lo, hi, err := code.CallPair(words...)
	if err != nil {
		t.Fatal(err)
	}
	return execution.Wide(lo, hi)
}

var float64Specials = []uint64{
	math.Float64bits(1.5), math.Float64bits(-2.25), 0x8000000000000000, // -0
	1, 0x000fffffffffffff, // subnormals
	0x7ff8000000000001, 0xfff4000000000123, // quiet and signaling NaN payloads
	0x7ff0000000000000,
}

var float32Specials = []uint32{
	math.Float32bits(1.5), math.Float32bits(-2.25), 0x80000000, 1, 0x007fffff,
	0x7fc00001, 0xffa00123, 0x7f800000,
}

// snan reports a signaling NaN, which 386 x87 results quiet.
func snan64(x uint64) bool {
	return x&0x7ff0000000000000 == 0x7ff0000000000000 && x&0x000fffffffffffff != 0 && x&(1<<51) == 0
}
func snan32(x uint32) bool { return x&0x7f800000 == 0x7f800000 && x&0x007fffff != 0 && x&(1<<22) == 0 }

// TestFloatEntryIdentity returns a float argument unchanged, bit for bit,
// at 32 and 64 bits for both flavors.
func TestFloatEntryIdentity(t *testing.T) {
	for _, tier := range floatTiers {
		t.Run(tier.name, func(t *testing.T) {
			for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
				code64 := floatCode(t, tier.tier, floatFunc(flavor, compile.ContextNative, abi.FloatArgument(0)|abi.FloatResult64), func(b *nyanjit.Builder) {
					b.Return(b.Param())
				})
				for _, x := range float64Specials {
					got := callBits(t, code64, x)
					if got != x && !(runtime.GOARCH == "386" && flavor == abi.C && snan64(x) && got == x|1<<51) {
						t.Errorf("flavor %d: f64 identity(%#x) = %#x", flavor, x, got)
					}
				}
				code32 := floatCode(t, tier.tier, floatFunc(flavor, compile.ContextNative, abi.FloatArgument(0)|abi.FloatResult32), func(b *nyanjit.Builder) {
					b.Return(b.ParamBits(32))
				})
				for _, x := range float32Specials {
					got := uint32(callBits(t, code32, x))
					if got != x && !(runtime.GOARCH == "386" && flavor == abi.C && snan32(x) && got == x|1<<22) {
						t.Errorf("flavor %d: f32 identity(%#x) = %#x", flavor, x, got)
					}
				}
			}
		})
	}
}

// TestFloatEntryMixed checks that integer and float arguments use
// independent register sequences (and ppc64le C positional integer
// registers): f(a int, x float32, b int, y float64).
func TestFloatEntryMixed(t *testing.T) {
	floats := abi.FloatArgument(1) | abi.FloatArgument(3)
	for _, tier := range floatTiers {
		t.Run(tier.name, func(t *testing.T) {
			for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
				params := func(b *nyanjit.Builder) (a, x, c, y nyanjit.Value) {
					return b.Param(), b.ParamBits(32), b.Param(), b.Param()
				}
				ints := floatCode(t, tier.tier, floatFunc(flavor, compile.ContextNative, floats), func(b *nyanjit.Builder) {
					a, _, c, _ := params(b)
					b.Return(b.Add(b.Mul(a, b.Const(1000)), c))
				})
				sum := floatCode(t, tier.tier, floatFunc(flavor, compile.ContextNative, floats|abi.FloatResult64), func(b *nyanjit.Builder) {
					a, x, c, y := params(b)
					b.Return(b.FAdd(b.FAdd(b.FConvert(x, 64), y), b.FFromSigned(b.Sub(a, c), 64)))
				})
				single := floatCode(t, tier.tier, floatFunc(flavor, compile.ContextNative, floats|abi.FloatResult32), func(b *nyanjit.Builder) {
					_, x, _, _ := params(b)
					b.Return(x)
				})
				if got := callBits(t, ints, uint64(7), math.Float32bits(1.5), uint64(9), math.Float64bits(2.5)); got != 7009 {
					t.Errorf("flavor %d: integer arguments = %d, want 7009", flavor, got)
				}
				if got := math.Float64frombits(callBits(t, sum, uint64(10), math.Float32bits(1.5), uint64(4), math.Float64bits(2.5))); got != 10 {
					t.Errorf("flavor %d: 1.5+2.5+(10-4) = %v", flavor, got)
				}
				if got := uint32(callBits(t, single, uint64(1), uint32(0x7fa00001), uint64(2), uint64(0))); got != 0x7fa00001 && !(runtime.GOARCH == "386" && flavor == abi.C) {
					t.Errorf("flavor %d: float32 argument = %#x", flavor, got)
				}
			}
		})
	}
}

// TestFloatEntryControlFlow returns |x| through two return paths.
func TestFloatEntryControlFlow(t *testing.T) {
	for _, tier := range floatTiers {
		t.Run(tier.name, func(t *testing.T) {
			code := floatCode(t, tier.tier, floatFunc(abi.C, compile.ContextNative, abi.FloatArgument(1)|abi.FloatResult64), func(b *nyanjit.Builder) {
				bias, x := b.Param(), b.Param()
				negative := b.NewLabel()
				b.Branch(b.FComparePredicate(x, ir.FloatLess, b.Const(0)), negative)
				b.Return(b.FAdd(x, b.FFromSigned(bias, 64)))
				b.Bind(negative)
				b.Return(b.FSub(b.FFromSigned(bias, 64), x))
			})
			for _, c := range []struct{ x, want float64 }{{2, 5}, {-2, 5}, {0.5, 3.5}} {
				if got := math.Float64frombits(callBits(t, code, uint64(3), math.Float64bits(c.x))); got != c.want {
					t.Errorf("f(3, %v) = %v, want %v", c.x, got, c.want)
				}
			}
		})
	}
}

// TestFloatEntryFunc calls float entries through Code.Func.
func TestFloatEntryFunc(t *testing.T) {
	for _, tier := range floatTiers {
		t.Run(tier.name, func(t *testing.T) {
			square := floatCode(t, tier.tier, floatFunc(abi.Go, compile.ContextNative, abi.FloatArgument(0)|abi.FloatResult64), func(b *nyanjit.Builder) {
				x := b.Param()
				b.Return(b.FMul(x, x))
			})
			var f func(float64) float64
			if err := square.Func(&f); err != nil {
				t.Fatal(err)
			}
			if got := f(1.5); got != 2.25 {
				t.Errorf("square(1.5) = %v", got)
			}
			if runtime.GOARCH == "386" {
				return // a Go ABI0 float32 result is not a float64 slot
			}
			scale := floatCode(t, tier.tier, floatFunc(abi.Go, compile.ContextNative, abi.FloatArgument(1)|abi.FloatResult32), func(b *nyanjit.Builder) {
				n, x := b.Param(), b.ParamBits(32)
				b.Return(b.FMul(x, b.FFromSigned(n, 32)))
			})
			var g func(int64, float32) float32
			if err := scale.Func(&g); err != nil {
				t.Fatal(err)
			}
			if got := g(3, 0.5); got != 1.5 {
				t.Errorf("scale(3, 0.5) = %v", got)
			}
		})
	}
}

// TestFloatEntryValidation rejects float marks that do not fit the program.
func TestFloatEntryValidation(t *testing.T) {
	for _, tier := range floatTiers {
		t.Run(tier.name, func(t *testing.T) {
			target, err := compile.NewTarget(compile.TargetSpec{Tier: tier.tier})
			if err != nil {
				t.Fatal(err)
			}
			for name, c := range map[string]struct {
				floats abi.FloatPassing
				build  func(*nyanjit.Builder)
			}{
				"beyond arity": {abi.FloatArgument(1), func(b *nyanjit.Builder) { b.Return(b.Param()) }},
				"16-bit param": {abi.FloatArgument(0), func(b *nyanjit.Builder) { b.Return(b.ParamBits(16)) }},
				"result width": {abi.FloatResult32, func(b *nyanjit.Builder) { b.Return(b.Param()) }},
				"both results": {abi.FloatResult32 | abi.FloatResult64, func(b *nyanjit.Builder) { b.Return(b.Param()) }},
			} {
				b := nyanjit.NewBuilderFor(target, floatFunc(abi.C, compile.ContextNative, c.floats))
				c.build(b)
				if _, err := b.Lower(); !errors.Is(err, compile.ErrInvalidFunction) {
					t.Errorf("%s: %v, want ErrInvalidFunction", name, err)
				}
			}
		})
	}
}

// libm opens the C math library, or skips.
func libm(t *testing.T, names ...string) []uintptr {
	t.Helper()
	handle, err := purego.Dlopen("libm.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		t.Skipf("libm: %v", err)
	}
	t.Cleanup(func() { purego.Dlclose(handle) })
	addresses := make([]uintptr, len(names))
	for i, name := range names {
		if addresses[i], err = purego.Dlsym(handle, name); err != nil {
			t.Skipf("%s: %v", name, err)
		}
	}
	return addresses
}

// TestFloatCallLibm calls libm fma(double, double, double),
// ldexp(double, int) and fmaf, from foreign and Go-managed code.
func TestFloatCallLibm(t *testing.T) {
	fns := libm(t, "fma", "ldexp", "fmaf")
	fma, ldexp, fmaf := fns[0], fns[1], fns[2]
	contexts := []compile.Context{compile.ContextForeign}
	if goregister.Supported() {
		contexts = append(contexts, compile.ContextGoManaged)
	}
	for _, tier := range floatTiers {
		t.Run(tier.name, func(t *testing.T) {
			for _, context := range contexts {
				code := floatCode(t, tier.tier, floatFunc(abi.C, context, 0), func(b *nyanjit.Builder) {
					x, y, z, e := b.Param(), b.Param(), b.Param(), b.ParamBits(32)
					r := b.CallCWithFloats(b.ConstNative(fma), abi.FloatArgument(0)|abi.FloatArgument(1)|abi.FloatArgument(2)|abi.FloatResult64, x, y, z)
					b.Return(b.CallCWithFloats(b.ConstNative(ldexp), abi.FloatArgument(0)|abi.FloatResult64, r, e))
				})
				// fma(3, 0.5, 0.25) = 1.75; ldexp(1.75, 3) = 14.
				if got := math.Float64frombits(callBits(t, code, math.Float64bits(3), math.Float64bits(0.5), math.Float64bits(0.25), uint32(3))); got != 14 {
					t.Errorf("context %d: ldexp(fma(3, .5, .25), 3) = %v", context, got)
				}
				single := floatCode(t, tier.tier, floatFunc(abi.C, context, abi.FloatArgument(0)|abi.FloatResult32), func(b *nyanjit.Builder) {
					x := b.ParamBits(32)
					b.Return(b.CallCWithFloats(b.ConstNative(fmaf), abi.FloatArgument(0)|abi.FloatArgument(1)|abi.FloatArgument(2)|abi.FloatResult32, x, x, b.ConstBits(32, uint64(math.Float32bits(-1)))))
				})
				if got := math.Float32frombits(uint32(callBits(t, single, math.Float32bits(3)))); got != 8 {
					t.Errorf("context %d: fmaf(3, 3, -1) = %v", context, got)
				}
			}
		})
	}
}

// TestFloatCallVariadic calls snprintf(buf, n, "%g %d %g", 1.5, 7, -0.25):
// amd64 passes the XMM argument count in AL.
func TestFloatCallVariadic(t *testing.T) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" && runtime.GOARCH != "386" {
		// A variadic double travels in integer registers on riscv64 and
		// loong64, and in both register files on ppc64le.
		t.Skip("variadic float arguments follow a different rule")
	}
	handle, err := purego.Dlopen("libc.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		t.Skipf("libc: %v", err)
	}
	defer purego.Dlclose(handle)
	snprintf, err := purego.Dlsym(handle, "snprintf")
	if err != nil {
		t.Skip(err)
	}
	if runtime.GOARCH == "arm64" {
		t.Skip("Linux arm64 passes variadic doubles in V registers, like prototyped ones; covered by amd64 under QEMU")
	}
	for _, tier := range floatTiers {
		t.Run(tier.name, func(t *testing.T) {
			code := floatCode(t, tier.tier, floatFunc(abi.C, compile.ContextForeign, 0), func(b *nyanjit.Builder) {
				buf, format := b.ParamNative(), b.ParamNative()
				r := b.CallCWithFloats(b.ConstNative(snprintf), abi.FloatArgument(3)|abi.FloatArgument(5),
					buf, b.ConstNative(64), format, b.Const(math.Float64bits(1.5)), b.ConstBits(32, 7), b.Const(math.Float64bits(-0.25)))
				b.Return(r)
			})
			buffer := make([]byte, 64)
			format := []byte("%g %d %g\x00")
			n, err := code.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&format[0])))
			runtime.KeepAlive(buffer)
			runtime.KeepAlive(format)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(buffer[:int32(n)]); got != "1.5 7 -0.25" {
				t.Errorf("snprintf = %q", got)
			}
		})
	}
}

var testFloatGoTarget = func(x float64, n int64) float64 { return x*float64(n) + 0.5 }

func floatGoTarget(x float64, n int64) float64 { return testFloatGoTarget(x, n) }

// TestFloatCallGo calls a Go func(float64, int64) float64 from Go-managed
// code.
func TestFloatCallGo(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("Go runtime registration is unavailable")
	}
	address := reflect.ValueOf(floatGoTarget).Pointer()
	for _, tier := range floatTiers {
		t.Run(tier.name, func(t *testing.T) {
			for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
				code := floatCode(t, tier.tier, floatFunc(flavor, compile.ContextGoManaged, abi.FloatArgument(0)|abi.FloatResult64), func(b *nyanjit.Builder) {
					x, n := b.Param(), b.Param()
					r := b.CallGoWithFloats(b.ConstNative(address), abi.FloatArgument(0)|abi.FloatResult64, x, n)
					b.Return(b.FAdd(r, x))
				})
				// 1.25*4 + 0.5 + 1.25 = 6.75
				if got := math.Float64frombits(callBits(t, code, math.Float64bits(1.25), uint64(4))); got != 6.75 {
					t.Errorf("flavor %d: f(1.25, 4) = %v", flavor, got)
				}
			}
		})
	}
}

// TestFloatCallListing checks the argument moves into FP registers on every
// target and the amd64 AL count.
func TestFloatCallListing(t *testing.T) {
	// The two float arguments land in the first two FP argument registers.
	want := map[string][]string{
		"amd64":   {"xmm0,", "xmm1,", "mov EAX, 2", "call R11"},
		"arm64":   {"D0,", "D1,", "blr"},
		"riscv64": {"f10,", "f11,", "jalr"},
		"ppc64le": {"f1,|vs1,", "f2,|vs2,", "bctrl"},
		"loong64": {"f0,", "f1,", "jirl"},
		"386":     {"fstp qword", "call EAX"},
	}
	for _, tier := range floatTiers {
		t.Run(tier.name, func(t *testing.T) {
			for arch, needles := range want {
				target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch, Tier: tier.tier})
				if err != nil {
					t.Fatal(err)
				}
				if (arch == "386") != (runtime.GOARCH == "386") {
					continue // the Builder's call targets are host-native words
				}
				b := nyanjit.NewBuilderFor(target, floatFunc(abi.C, compile.ContextForeign, 0))
				x, n := b.Param(), b.Param()
				b.Return(b.CallCWithFloats(b.ConstNative(0x1000), abi.FloatArgument(0)|abi.FloatArgument(2)|abi.FloatResult64, x, n, x))
				u, err := b.Lower()
				if err != nil {
					t.Fatalf("%s: %v", arch, err)
				}
				listing, err := u.Listing()
				if err != nil {
					t.Fatalf("%s: %v", arch, err)
				}
				for _, needle := range needles {
					found := false
					for _, alternative := range strings.Split(needle, "|") {
						found = found || strings.Contains(strings.ToLower(listing), strings.ToLower(alternative))
					}
					if !found {
						t.Errorf("%s: listing lacks %q:\n%s", arch, needle, listing)
					}
				}
			}
		})
	}
}

// TestFloatEntryReport names the FP registers of a float entry in its
// report on every target: f(a int, x float64) float64.
func TestFloatEntryReport(t *testing.T) {
	floats := abi.FloatArgument(1) | abi.FloatResult64
	for _, tier := range floatTiers {
		t.Run(tier.name, func(t *testing.T) {
			for _, arch := range []string{"amd64", "arm64", "386", "riscv64", "ppc64le", "loong64"} {
				for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
					target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch, Tier: tier.tier})
					if err != nil {
						t.Fatal(err)
					}
					b := nyanjit.NewBuilderFor(target, floatFunc(flavor, compile.ContextNative, floats))
					a, x := b.Param(), b.Param()
					b.Return(b.FAdd(x, b.FFromSigned(a, 64)))
					u, err := b.Lower()
					if err != nil {
						t.Fatalf("%s/%d: %v", arch, flavor, err)
					}
					report := u.Report()
					words := []int{1, 1}
					if arch == "386" {
						words = []int{2, 2}
					}
					want, err := abi.PreflightCall(abi.CallSpec{Architecture: arch, Kind: abi.FunctionCall, ABI: flavor, ArgumentCount: 2, ArgumentWords: words, Floats: floats})
					if err != nil {
						t.Fatal(err)
					}
					locations := report.ArgumentLocations()
					if arch == "386" {
						// Entry stack offsets count the return address; only
						// a C result changes.
						want.ArgumentLocations = locations
						if flavor == abi.Go {
							want.ResultLocation = abi.ValueLocation{Words: []abi.WordLocation{{Storage: abi.Stack, StackOffset: 20}, {Storage: abi.Stack, StackOffset: 24}}}
						}
					}
					if report.Floats() != floats || len(locations) != 2 || !reflect.DeepEqual(locations, want.ArgumentLocations) ||
						!reflect.DeepEqual(report.ResultLocation(), want.ResultLocation) {
						t.Errorf("%s/%d: report floats %#x arguments %+v result %+v, want %+v %+v", arch, flavor, uint64(report.Floats()),
							locations, report.ResultLocation(), want.ArgumentLocations, want.ResultLocation)
					}
				}
			}
		})
	}
}
