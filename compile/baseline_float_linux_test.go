//go:build linux && (arm64 || amd64 || riscv64 || ppc64le || loong64 || 386)

package compile_test

import (
	"errors"
	"math"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/compile"
)

// Float passing at the baseline tier: generated callees with Func.Floats
// entered by generated C callers, Go and C callees, and libm.

var bfTiers = []compile.Tier{compile.TierBaseline, compile.TierOptimized}

func bfStack() compile.StackPolicy {
	if runtime.GOARCH == "386" {
		return compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}
	}
	return compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 512}
}

// bfCode lowers build at tier with f and binds it.
func bfCode(t *testing.T, tier compile.Tier, f compile.Func, build func(*n.Builder)) *execution.Code {
	t.Helper()
	if f.Context == compile.ContextGoManaged && !goregister.Supported() {
		t.Skip("Go runtime registration is unavailable")
	}
	target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
	if err != nil {
		t.Fatal(err)
	}
	b := n.NewBuilderFor(target, f)
	build(b)
	u, err := b.Lower()
	if err != nil {
		t.Fatalf("tier %d: lower: %v", tier, err)
	}
	tmpl, err := u.Template()
	if err != nil {
		t.Fatalf("tier %d: template: %v", tier, err)
	}
	code, err := tmpl.Bind(nil)
	if err != nil {
		t.Fatalf("tier %d: bind: %v", tier, err)
	}
	t.Cleanup(func() { code.Close() })
	return code
}

// bfCaller returns a ContextForeign C entry taking a C target and the
// raw argument bits (widths bits) that calls the target with floats.
func bfCaller(t *testing.T, tier compile.Tier, floats abi.FloatPassing, bits []uint8) *execution.Code {
	return bfCode(t, tier, compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: bfStack()}, func(b *n.Builder) {
		target := b.ParamNative()
		args := make([]n.Value, len(bits))
		for i, w := range bits {
			args[i] = b.ParamBits(w)
		}
		b.Return(b.CallCWithFloats(target, floats, args...))
	})
}

// bfCall calls code (a bfCaller) with target and args, returning the
// result bits; widths gives each argument's width.
func bfCall(t *testing.T, code *execution.Code, target uintptr, widths []uint8, args ...uint64) uint64 {
	t.Helper()
	words := []any{target}
	for i, x := range args {
		if widths[i] == 32 {
			words = append(words, uint32(x))
		} else {
			words = append(words, x)
		}
	}
	lo, hi, err := code.CallPair(execution.Words(words...)...)
	if err != nil {
		t.Fatal(err)
	}
	return execution.Wide(lo, hi)
}

var (
	bf64Specials = []uint64{
		math.Float64bits(1.25), math.Float64bits(-3.5e300), 0x8000000000000000, // -0
		0x0000000000000001, 0x000fffffffffffff, // subnormals
		0x7ff8000000000123, 0xfff0000000000001, // quiet and signaling NaN with payloads
		0x7ff0000000000000,
	}
	bf32Specials = []uint64{
		uint64(math.Float32bits(1.25)), uint64(math.Float32bits(-7e30)), 0x80000000,
		0x00000001, 0x007fffff, 0x7fc00123, 0xff800001, 0x7f800000,
	}
)

// bfSignaling reports whether bits (w wide) is a signaling NaN, which a
// 386 x87 C result quiets.
func bfSignaling(bits uint64, w uint8) bool {
	if w == 32 {
		return bits&0x7f800000 == 0x7f800000 && bits&0x7fffff != 0 && bits&0x400000 == 0
	}
	return bits&0x7ff0000000000000 == 0x7ff0000000000000 && bits&0xfffffffffffff != 0 && bits&0x8000000000000 == 0
}

func bfEntry(t *testing.T, c *execution.Code) uintptr {
	t.Helper()
	address, err := c.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	return address
}

// TestBaselineFloatEntriesAndCalls compiles C entries with float passing at
// each tier and calls them from generated C callers of each tier.
func TestBaselineFloatEntriesAndCalls(t *testing.T) {
	native := compile.Func{ABI: abi.C, Stack: bfStack()}
	type entry struct {
		name   string
		floats abi.FloatPassing
		bits   []uint8
		build  func(*n.Builder)
		inputs [][]uint64
		want   func([]uint64) uint64
		// exact results pass a value through unchanged
		exact bool
	}
	result64 := abi.FloatArgument(0) | abi.FloatResult64
	result32 := abi.FloatArgument(0) | abi.FloatResult32
	mixed := abi.FloatArgument(1) | abi.FloatArgument(3)
	var entries []entry
	for _, x := range bf64Specials {
		entries = append(entries, entry{"id64", result64, []uint8{64}, func(b *n.Builder) { b.Return(b.ParamBits(64)) }, [][]uint64{{x}}, func(in []uint64) uint64 { return in[0] }, true})
	}
	for _, x := range bf32Specials {
		entries = append(entries, entry{"id32", result32, []uint8{32}, func(b *n.Builder) { b.Return(b.ParamBits(32)) }, [][]uint64{{x}}, func(in []uint64) uint64 { return in[0] }, true})
	}
	entries = append(entries,
		entry{"arith64", result64, []uint8{64}, func(b *n.Builder) {
			x := b.ParamBits(64)
			b.Return(b.FAdd(b.FMul(x, b.Float64(2)), b.Float64(0.5)))
		}, [][]uint64{{math.Float64bits(3)}, {math.Float64bits(-1e10)}}, func(in []uint64) uint64 {
			return math.Float64bits(math.Float64frombits(in[0])*2 + 0.5)
		}, false},
		entry{"mixed-int", mixed, []uint8{64, 32, 64, 64}, func(b *n.Builder) {
			a, _, c, _ := b.Param(), b.ParamBits(32), b.Param(), b.ParamBits(64)
			b.Return(b.Add(b.Mul(a, b.Const(3)), c))
		}, [][]uint64{{5, uint64(math.Float32bits(1)), 7, math.Float64bits(2)}}, func(in []uint64) uint64 { return in[0]*3 + in[2] }, false},
		entry{"mixed-f64", mixed | abi.FloatResult64, []uint8{64, 32, 64, 64}, func(b *n.Builder) {
			a, x, c, y := b.Param(), b.ParamBits(32), b.Param(), b.ParamBits(64)
			s := b.FAdd(y, b.FConvert(x, 64))
			b.Return(b.FAdd(s, b.FFromSigned(b.Sub(a, c), 64)))
		}, [][]uint64{{10, uint64(math.Float32bits(1.5)), 3, math.Float64bits(0.25)}}, func(in []uint64) uint64 {
			return math.Float64bits(math.Float64frombits(in[3]) + float64(math.Float32frombits(uint32(in[1]))) + float64(int64(in[0]-in[2])))
		}, false},
		entry{"mixed-f32", mixed | abi.FloatResult32, []uint8{64, 32, 64, 64}, func(b *n.Builder) {
			b.Param()
			x := b.ParamBits(32)
			b.Param()
			b.ParamBits(64)
			b.Return(x)
		}, [][]uint64{{1, 0x7fc00777, 2, 3}, {1, 0x80000000, 2, 3}}, func(in []uint64) uint64 { return in[1] }, true},
		entry{"eight", 0xff | abi.FloatResult64, []uint8{64, 64, 64, 64, 64, 64, 64, 64}, func(b *n.Builder) {
			s := b.ParamBits(64)
			for range 7 {
				s = b.FSub(s, b.ParamBits(64))
			}
			b.Return(s)
		}, [][]uint64{{bf64(100), bf64(1), bf64(2), bf64(3), bf64(4), bf64(5), bf64(6), bf64(7)}}, func([]uint64) uint64 { return bf64(72) }, false},
	)
	for _, e := range entries {
		if len(e.bits) > abi.MaxArgumentsFor(runtime.GOARCH)-1 {
			continue // the caller takes the target too
		}
		for _, calleeTier := range bfTiers {
			callee := bfCode(t, calleeTier, compile.Func{ABI: native.ABI, Stack: native.Stack, Floats: e.floats}, e.build)
			for _, callerTier := range bfTiers {
				caller := bfCaller(t, callerTier, e.floats, e.bits)
				for _, in := range e.inputs {
					want := e.want(in)
					if e.floats.ResultBits() == 32 {
						want &= 0xffffffff
					}
					if e.exact && runtime.GOARCH == "386" && bfSignaling(want, e.floats.ResultBits()) {
						continue // FLD quiets it
					}
					if got := bfCall(t, caller, bfEntry(t, callee), e.bits, in...); got != want {
						t.Errorf("%s callee tier %d caller tier %d %#x: got %#x, want %#x", e.name, calleeTier, callerTier, in, got, want)
					}
				}
			}
		}
	}
}

func bf64(x float64) uint64 { return math.Float64bits(x) }

func bfGoScale(x float64, k int64) float64        { return x*float64(k) + 0.5 }
func bfGoNarrow(x float32, y float64) float32     { return x + float32(y) }
func bfGoID32(x float32) float32                  { return x }
func bfGoMix(a int64, x float64, b int64) float64 { return x - float64(a*b) }

// TestBaselineFloatGoCalls calls Go functions with float parameters and
// results from Go-managed code.
func TestBaselineFloatGoCalls(t *testing.T) {
	type call struct {
		name   string
		fn     any
		floats abi.FloatPassing
		bits   []uint8
		inputs [][]uint64
		want   func([]uint64) uint64
	}
	calls := []call{
		{"scale", bfGoScale, abi.FloatArgument(0) | abi.FloatResult64, []uint8{64, 64}, [][]uint64{{bf64(3), 4}, {bf64(-0.25), 1 << 40}},
			func(in []uint64) uint64 { return bf64(bfGoScale(math.Float64frombits(in[0]), int64(in[1]))) }},
		{"narrow", bfGoNarrow, abi.FloatArgument(0) | abi.FloatArgument(1) | abi.FloatResult32, []uint8{32, 64}, [][]uint64{{uint64(math.Float32bits(1.5)), bf64(2.25)}},
			func(in []uint64) uint64 {
				return uint64(math.Float32bits(bfGoNarrow(math.Float32frombits(uint32(in[0])), math.Float64frombits(in[1]))))
			}},
		{"id32", bfGoID32, abi.FloatArgument(0) | abi.FloatResult32, []uint8{32}, [][]uint64{{0x7fc00123}, {0x80000000}, {1}},
			func(in []uint64) uint64 { return in[0] }},
		{"mix", bfGoMix, abi.FloatArgument(1) | abi.FloatResult64, []uint8{64, 64, 64}, [][]uint64{{3, bf64(100), 5}},
			func(in []uint64) uint64 {
				return bf64(bfGoMix(int64(in[0]), math.Float64frombits(in[1]), int64(in[2])))
			}},
	}
	for name, f := range managedFuncs() {
		for _, c := range calls {
			target := reflect.ValueOf(c.fn).Pointer()
			for _, tier := range bfTiers {
				code := bfCode(t, tier, f, func(b *n.Builder) {
					args := make([]n.Value, len(c.bits))
					for i, w := range c.bits {
						args[i] = b.ParamBits(w)
					}
					b.Return(b.CallGoWithFloats(b.ConstNative(target), c.floats, args...))
				})
				for _, in := range c.inputs {
					words := make([]any, len(in))
					for i, x := range in {
						if c.bits[i] == 32 {
							words[i] = uint32(x)
						} else {
							words[i] = x
						}
					}
					lo, hi, err := code.CallPair(execution.Words(words...)...)
					if err != nil {
						t.Fatal(err)
					}
					if got, want := execution.Wide(lo, hi), c.want(in); got != want {
						t.Errorf("%s %s tier %d %#x: got %#x, want %#x", name, c.name, tier, in, got, want)
					}
				}
			}
		}
	}
}

// libm returns the address of a libm function, or skips.
func bfLibm(t *testing.T, name string) uintptr {
	t.Helper()
	h, err := purego.Dlopen("libm.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		t.Skipf("libm: %v", err)
	}
	address, err := purego.Dlsym(h, name)
	if err != nil {
		t.Skipf("%s: %v", name, err)
	}
	return address
}

func bfLibc(t *testing.T, name string) uintptr {
	t.Helper()
	h, err := purego.Dlopen("libc.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		t.Skipf("libc: %v", err)
	}
	address, err := purego.Dlsym(h, name)
	if err != nil {
		t.Skipf("%s: %v", name, err)
	}
	return address
}

// TestBaselineFloatLibm calls libm's fma and ldexp from foreign and
// Go-managed code (the managed C call goes through the float adapter).
func TestBaselineFloatLibm(t *testing.T) {
	fma, ldexp := bfLibm(t, "fma"), bfLibm(t, "ldexp")
	contexts := map[string]compile.Func{
		"foreign": {ABI: abi.C, Context: compile.ContextForeign, Stack: bfStack()},
	}
	for name, f := range managedFuncs() {
		contexts["managed-"+name] = f
	}
	for name, f := range contexts {
		t.Run(name, func(t *testing.T) { bfLibmContext(t, f, fma, ldexp) })
	}
}

func bfLibmContext(t *testing.T, f compile.Func, fma, ldexp uintptr) {
	for _, tier := range bfTiers {
		fmaCode := bfCode(t, tier, f, func(b *n.Builder) {
			x, y, z := b.ParamBits(64), b.ParamBits(64), b.ParamBits(64)
			b.Return(b.CallCWithFloats(b.ConstNative(fma), 7|abi.FloatResult64, x, y, z))
		})
		ldexpCode := bfCode(t, tier, f, func(b *n.Builder) {
			// A C int is a sign-extended word (ppc64le requires it); 386 passes one stack word.
			x, e := b.ParamBits(64), b.ParamBits(uint8(strconv.IntSize))
			b.Return(b.CallCWithFloats(b.ConstNative(ldexp), abi.FloatArgument(0)|abi.FloatResult64, x, e))
		})
		for _, in := range [][3]float64{{2, 3, 4}, {1e308, 10, -math.Inf(1)}, {0.1, 0.1, -0.01}} {
			lo, hi, err := fmaCode.CallPair(execution.Words(bf64(in[0]), bf64(in[1]), bf64(in[2]))...)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := execution.Wide(lo, hi), bf64(math.FMA(in[0], in[1], in[2])); got != want {
				t.Errorf("tier %d fma%v = %#x, want %#x", tier, in, got, want)
			}
		}
		for _, in := range []struct {
			x float64
			e int32
		}{{1.5, 10}, {3, -1074}, {-1, 2000}} {
			lo, hi, err := ldexpCode.CallPair(execution.Words(bf64(in.x), int(in.e))...)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := execution.Wide(lo, hi), bf64(math.Ldexp(in.x, int(in.e))); got != want {
				t.Errorf("tier %d ldexp(%v, %d) = %#x, want %#x", tier, in.x, in.e, got, want)
			}
		}
	}
}

// TestBaselineFloatVariadic calls snprintf("%g", x), whose double travels in
// an FP register on amd64 (with AL counting it) and arm64.
func TestBaselineFloatVariadic(t *testing.T) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("a variadic double travels in general registers or stack words")
	}
	snprintf := bfLibc(t, "snprintf")
	format := []byte("%g|%g\x00")
	for _, tier := range bfTiers {
		code := bfCode(t, tier, compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: bfStack()}, func(b *n.Builder) {
			buf, size, fmt, x, y := b.ParamNative(), b.Param(), b.ParamNative(), b.ParamBits(64), b.ParamBits(64)
			b.Return(b.CallCWithFloats(b.ConstNative(snprintf), abi.FloatArgument(3)|abi.FloatArgument(4), buf, size, fmt, x, y))
		})
		buf := make([]byte, 64)
		got, err := code.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&format[0])), uintptr(bf64(2.5)), uintptr(bf64(-1e-300)))
		runtime.KeepAlive(buf)
		runtime.KeepAlive(format)
		if err != nil {
			t.Fatal(err)
		}
		if s := string(buf[:got]); s != "2.5|-1e-300" {
			t.Errorf("tier %d: snprintf wrote %q", tier, s)
		}
	}
}

// TestBaselineFloatListing checks the amd64 AL count and the FP argument
// registers in a cross-target listing.
func TestBaselineFloatListing(t *testing.T) {
	if runtime.GOARCH == "386" {
		t.Skip("an amd64 call target needs a 64-bit Builder")
	}
	target, err := compile.NewTarget(compile.TargetSpec{Architecture: "amd64", Tier: compile.TierBaseline})
	if err != nil {
		t.Fatal(err)
	}
	b := n.NewBuilderFor(target, compile.Func{ABI: abi.C, Context: compile.ContextExternal, Stack: bfStack(), Floats: abi.FloatArgument(1) | abi.FloatResult64})
	fn, x := b.Param(), b.ParamBits(64)
	b.Return(b.CallCWithFloats(fn, abi.FloatArgument(0)|abi.FloatArgument(1)|abi.FloatResult64, x, b.Float64(2)))
	u, err := b.Lower()
	if err != nil {
		t.Fatal(err)
	}
	listing, err := u.Listing()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listing, "mov EAX, 2") || !strings.Contains(listing, "call R11") {
		t.Errorf("listing lacks AL=2 before the call:\n%s", listing)
	}
	r := u.Report()
	if r.Floats() != abi.FloatArgument(1)|abi.FloatResult64 || r.ResultRegister() != "XMM0" || abi.SingleRegister(r.ArgumentLocations()[1]) != "XMM0" || abi.SingleRegister(r.ArgumentLocations()[0]) != "RDI" {
		t.Errorf("report: %+v", r.Snapshot())
	}
}

// TestBaselineFloatValidation checks Func.Floats against the program and the
// float call limits.
func TestBaselineFloatValidation(t *testing.T) {
	target, err := compile.NewTarget(compile.TargetSpec{Tier: compile.TierBaseline})
	if err != nil {
		t.Fatal(err)
	}
	lower := func(f compile.Func, build func(*n.Builder)) error {
		b := n.NewBuilderFor(target, f)
		build(b)
		_, err := b.Lower()
		return err
	}
	native := compile.Func{ABI: abi.C, Stack: bfStack()}
	for name, c := range map[string]struct {
		floats abi.FloatPassing
		build  func(*n.Builder)
	}{
		"narrow parameter":  {abi.FloatArgument(0), func(b *n.Builder) { b.Return(b.ParamBits(16)) }},
		"missing parameter": {abi.FloatArgument(1), func(b *n.Builder) { b.Return(b.Param()) }},
		"result width":      {abi.FloatResult32, func(b *n.Builder) { b.Return(b.Param()) }},
		"both results":      {abi.FloatResult32 | abi.FloatResult64, func(b *n.Builder) { b.Return(b.ParamBits(32)) }},
	} {
		f := native
		f.Floats = c.floats
		if err := lower(f, c.build); !errors.Is(err, compile.ErrInvalidFunction) {
			t.Errorf("%s: got %v, want ErrInvalidFunction", name, err)
		}
	}
	// A Go-managed float C call passes the adapter: one argument fewer.
	managed := managedFuncs()["C"]
	limit := min(8, abi.MaxArgumentsFor(runtime.GOARCH)) - 1
	for _, k := range []int{limit, limit + 1} {
		err := lower(managed, func(b *n.Builder) {
			args := make([]n.Value, k)
			for i := range args {
				args[i] = b.Const(uint64(i))
			}
			args[0] = b.Float64(1)
			b.Return(b.CallCWithFloats(b.ConstNative(1), abi.FloatArgument(0), args...))
		})
		if (err == nil) != (k == limit) || err != nil && !errors.Is(err, compile.ErrBackendLimit) {
			t.Errorf("managed float C call with %d arguments: %v", k, err)
		}
	}
}
