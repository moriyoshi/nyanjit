//go:build !nyanjit_no_goregister && linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"errors"
	"reflect"
	"runtime"
	"strconv"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/abicheck"
	"github.com/moriyoshi/nyanjit/instrument/irconform"
	"github.com/moriyoshi/nyanjit/ir"
)

// TestBaselineManagedRuntime runs the other Go-managed runtime tests at
// TierBaseline.
func TestBaselineManagedRuntime(t *testing.T) {
	atBaseline(t, map[string]func(*testing.T){
		"GeneratedGoCallNestingRejected": TestGeneratedGoCallNestingRejected,
		"GeneratedGoCallRecursion":       TestGeneratedGoCallRecursionRejected,
		"FixedGoCallBindingsChecked":     TestFixedGoCallBindingsChecked,
	})
}

//go:noinline
func managedGrow(depth, x uint64) uint64 {
	var frame [512]byte
	frame[depth%512] = byte(x)
	if depth == 0 {
		runtime.GC()
		runtime.KeepAlive(&frame)
		return x*3 + 1
	}
	r := managedGrow(depth-1, x) + uint64(frame[depth%512]&1)
	runtime.KeepAlive(&frame)
	return r
}

// managedGrowCall grows the goroutine stack and collects garbage while a
// generated caller is on it.
func managedGrowCall(x uint64) uint64 { return managedGrow(48, x) }

func managedGrowWant(x uint64) uint64 { return x*3 + 1 + 48*uint64(byte(x)&1) }

var managedReentry *execution.Code

// managedReenter calls the generated function again from a grown stack.
func managedReenter(x uint64) uint64 {
	var frame [2048]byte
	frame[x%2048] = 1
	runtime.GC()
	r, err := managedReentry.Call(execution.Words(x - 1)...)
	if err != nil {
		panic(err)
	}
	runtime.KeepAlive(&frame)
	return uint64(r) + uint64(frame[x%2048])
}

// managedLiveCount is the number of integer values managedLive keeps live
// across its calls: fewer where a C entry's saves fill most of the
// managed frame.
func managedLiveCount() int {
	switch runtime.GOARCH {
	case "386":
		return 4
	case "ppc64le":
		return 2
	}
	return 20
}

// managedLive builds a Go-managed function that keeps integer and
// floating-point values and Vars live across Go calls in a loop, then a C
// call through the gateway when cAddress is not zero.
func managedLive(goAddress, cAddress uintptr) func(b *n.Builder) {
	return func(b *n.Builder) {
		x, y := b.Param(), b.Param()
		acc, i := b.Var(x), b.Var(b.Const(0))
		var live []n.Value
		for k := range managedLiveCount() {
			live = append(live, b.Add(b.Mul(x, b.Const(uint64(k+3))), y))
		}
		f := b.FMul(b.FFromUnsigned(y, 64), b.Float64(1.5))
		loop := b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		c := b.CallGo(b.ConstNative(goAddress), b.Add(b.Get(acc), b.Get(i)))
		b.Set(acc, b.Xor(c, b.Get(i)))
		b.Set(i, b.Add(b.Get(i), b.Const(1)))
		b.Branch(b.CompareImmediate(b.Get(i), ir.BranchBelow, 4), loop)
		sum := b.Get(acc)
		for _, v := range live {
			sum = b.Add(sum, v)
		}
		if cAddress != 0 {
			sum = b.CallC(b.ConstNative(cAddress), sum, live[1], b.Const(5))
		}
		b.Return(b.Add(sum, b.FToUnsigned(f, 64)))
	}
}

func managedLiveWant(x, y uint64, c bool) uint64 {
	acc := x
	for i := uint64(0); i < 4; i++ {
		acc = managedGrowWant(acc+i) ^ i
	}
	sum := acc
	for k := range managedLiveCount() {
		sum += x*uint64(k+3) + y
	}
	if c {
		sum = sum + 2*(x*4+y) + 3*5
	}
	return sum + uint64(float64(y)*1.5)
}

// managedTiers lowers build at both tiers, binds the code and returns it
// with the baseline unit's report. The optimized code is nil when its frame
// does not fit the stack budget, which the baseline's may.
func managedTiers(t *testing.T, f compile.Func, build func(*n.Builder)) (codes [2]*execution.Code, report abi.Report) {
	t.Helper()
	for k, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
		if err != nil {
			t.Fatal(err)
		}
		b := n.NewBuilderFor(target, f)
		build(b)
		u, err := b.Lower()
		if tier == compile.TierOptimized && errors.Is(err, compile.ErrStackRequired) {
			t.Logf("optimized tier: %v", err)
			continue
		}
		if err != nil {
			t.Fatalf("tier %d: %v", tier, err)
		}
		if _, err := u.RegisterScopes(); (tier == compile.TierBaseline) != errors.Is(err, compile.ErrUnsupported) {
			t.Fatalf("tier %d unit: RegisterScopes = %v", tier, err)
		}
		if !u.Report().GoManaged() {
			t.Fatalf("tier %d report is not Go-managed: %+v", tier, u.Report().Snapshot())
		}
		tmpl, err := u.Template()
		if errors.Is(err, compile.ErrBackendLimit) && runtime.GOARCH == "386" {
			t.Skipf("tier %d: %v", tier, err)
		}
		if err != nil {
			t.Fatalf("tier %d template: %v", tier, err)
		}
		code, err := tmpl.Bind(nil)
		if err != nil {
			t.Fatalf("tier %d bind: %v", tier, err)
		}
		codes[k] = code
		report = u.Report()
	}
	return codes, report
}

// TestBaselineManagedMatchesOptimized runs Go-managed functions whose Go
// callees grow the stack and collect garbage at both tiers and compares
// their results.
func TestBaselineManagedMatchesOptimized(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	cb := n.NewBuilder()
	a, b2, c := cb.Param(), cb.Param(), cb.Param()
	cb.Return(cb.Add(cb.Add(a, cb.Mul(b2, cb.Const(2))), cb.Mul(c, cb.Const(3))))
	cp, err := cb.Finish()
	if err != nil {
		t.Fatal(err)
	}
	// A plain C caller supplies no workspace: the callee spills to its stack.
	callee, err := n.Compile(cp, compile.TargetSpec{}, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}})
	if err != nil {
		t.Fatal(err)
	}
	defer callee.Close()
	cAddress, err := callee.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	goAddress := reflect.ValueOf(managedGrowCall).Pointer()
	for name, f := range managedFuncs() {
		for _, withC := range []bool{false, true} {
			t.Run(name+"/C="+strconv.FormatBool(withC), func(t *testing.T) {
				address := uintptr(0)
				if withC {
					address = cAddress
				}
				codes, _ := managedTiers(t, f, managedLive(goAddress, address))
				for _, args := range [][2]uint64{{1, 2}, {1000, 7}, {^uint64(0) >> 8, 99}} {
					want := managedLiveWant(args[0], args[1], withC)
					var got [2]uint64
					got[0] = want
					for k, code := range codes {
						if code == nil {
							continue
						}
						lo, hi, err := code.CallPair(execution.Words(args[0], args[1])...)
						if err != nil {
							t.Fatal(err)
						}
						got[k] = execution.Wide(lo, hi)
					}
					if got[0] != want || got[1] != want {
						t.Fatalf("%v: optimized %#x, baseline %#x, want %#x", args, got[0], got[1], want)
					}
				}
				for _, code := range codes {
					if code != nil {
						code.Close()
					}
				}
			})
		}
	}
}

// TestBaselineManagedReentry reenters a baseline Go-managed function from
// its own Go callee, growing the stack at each level, with values live
// across each call.
func TestBaselineManagedReentry(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	reenter := reflect.ValueOf(managedReenter).Pointer()
	for name, f := range managedFuncs() {
		t.Run(name, func(t *testing.T) {
			codes, _ := managedTiers(t, f, func(b *n.Builder) {
				x := b.Param()
				base := b.NewLabel()
				live := b.Mul(x, b.Const(7))
				b.Branch(b.IsZero(x), base)
				r := b.CallGo(b.ConstNative(reenter), x)
				b.Return(b.Add(r, live))
				b.Bind(base)
				b.Return(b.Const(1))
			})
			defer func() { managedReentry = nil }()
			want := uint64(1)
			for x := uint64(1); x <= 12; x++ {
				want += x*7 + 1
			}
			for k, code := range codes {
				if code == nil {
					continue
				}
				managedReentry = code
				lo, hi, err := code.CallPair(execution.Words(uint64(12))...)
				if got := execution.Wide(lo, hi); err != nil || got != want {
					t.Fatalf("tier %d: %d, %v; want %d", k, execution.Wide(lo, hi), err, want)
				}
				code.Close()
			}
		})
	}
}

// TestBaselineManagedLeafPreserves checks with an independent caller that a
// Go-managed baseline C leaf under register pressure restores every register
// the C ABI preserves.
func TestBaselineManagedLeafPreserves(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	target, err := compile.NewTarget(compile.TargetSpec{Tier: compile.TierBaseline})
	if err != nil {
		t.Fatal(err)
	}
	b := n.NewBuilderFor(target, managedFuncs()["C"])
	x := b.ParamNative()
	const count = 24
	vars := make([]n.Var, count)
	for k := range vars {
		vars[k] = b.Var(b.Add(x, b.ConstNative(uintptr(k))))
	}
	loop, done := b.NewLabel(), b.NewLabel()
	b.Jump(loop)
	b.Bind(loop)
	values := make([]n.Value, count)
	for k := range vars {
		values[k] = b.Get(vars[k])
	}
	for k := range vars {
		b.Set(vars[k], b.Add(b.Mul(values[k], b.ConstNative(3)), values[(k+1)%count]))
	}
	b.Jump(done)
	b.Bind(done)
	sum := b.Get(vars[0])
	for _, v := range vars[1:] {
		sum = b.Add(sum, b.Get(v))
	}
	b.Return(sum)
	u, err := b.Lower()
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := u.Template()
	if err != nil {
		t.Fatal(err)
	}
	res, err := abicheck.CheckPreserved(tmpl, execution.Words(uintptr(11))...)
	if errors.Is(err, abicheck.ErrUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if want := uintptr(4 * (count*11 + count*(count-1)/2)); res.Return != want {
		t.Fatalf("got %d want %d", res.Return, want)
	}
	if len(res.Clobbered) != 0 {
		t.Fatalf("callee-preserved register, g or SP changed: %v", res.Clobbered)
	}
}

// TestBaselineManagedCorpus runs a sample of the conformance corpus as
// registered Go-managed C and Go entries at TierBaseline and compares the
// results with the same tier's ContextNative code.
func TestBaselineManagedCorpus(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	target, err := compile.NewTarget(compile.TargetSpec{Tier: compile.TierBaseline})
	if err != nil {
		t.Fatal(err)
	}
	stack := managedFuncs()["C"].Stack
	native := compile.Func{ABI: abi.C, Stack: stack}
	ran := 0
	for k, c := range irconform.Cases() {
		// Registrations are permanent and budgeted per process.
		if k%8 != 0 || c.Buffer != 0 {
			continue
		}
		p, err := c.Build()
		if err != nil {
			continue
		}
		var widths []uint8
		for _, ins := range p.Instructions() {
			if ins.Op == ir.OpParam {
				widths = append(widths, ins.ValueType().LaneBits)
			}
		}
		bind := func(f compile.Func) *execution.Code {
			u, err := compile.Lower(target, p, f)
			if err != nil {
				return nil
			}
			tmpl, err := u.Template()
			if err != nil {
				return nil
			}
			code, err := tmpl.Bind(nil)
			if err != nil {
				t.Fatalf("%s: bind: %v", c.Name, err)
			}
			return code
		}
		reference := bind(native)
		if reference == nil {
			continue
		}
		for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
			f := compile.Func{ABI: flavor, Context: compile.ContextGoManaged, Stack: stack, AssumePointerFree: true}
			if flavor == abi.Go {
				native := native
				native.ABI = abi.Go
				reference.Close()
				if reference = bind(native); reference == nil {
					break
				}
			}
			code := bind(f)
			if code == nil {
				continue
			}
			ran++
			for _, input := range c.Inputs {
				words := make([]any, len(input))
				for i, x := range input {
					words[i] = managedArgument(widths[i], x)
				}
				wantLo, wantHi, werr := reference.CallPair(execution.Words(words...)...)
				lo, hi, err := code.CallPair(execution.Words(words...)...)
				if err != nil || werr != nil || lo != wantLo || hi != wantHi {
					t.Errorf("%s ABI %d %v: managed %#x:%#x %v, native %#x:%#x %v", c.Name, flavor, input, hi, lo, err, wantHi, wantLo, werr)
				}
			}
			code.Close()
		}
		if reference != nil {
			reference.Close()
		}
	}
	if ran == 0 {
		t.Fatal("no case ran")
	}
	t.Logf("%d managed entries ran", ran)
}

func managedArgument(bits uint8, x uint64) any {
	switch {
	case bits == 64:
		return x
	case bits == 32 && strconv.IntSize == 32:
		return uintptr(x)
	case bits == 32:
		return uint32(x)
	case bits == 16:
		return uint16(x)
	}
	return uint8(x)
}
