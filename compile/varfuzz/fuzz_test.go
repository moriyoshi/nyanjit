//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || 386 || loong64)

package varfuzz

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

var gcCounter atomic.Uint64

func maybeGC() {
	if gcCounter.Add(1)%211 == 0 {
		runtime.GC()
	}
}

//go:noinline
func goCallee1(a uintptr) uint64 { maybeGC(); return calleeFormula([]uint64{uint64(a)}) }

//go:noinline
func goCallee2(a, b uintptr) uint64 {
	maybeGC()
	return calleeFormula([]uint64{uint64(a), uint64(b)})
}

//go:noinline
func goCallee3(a, b, c uintptr) uint64 {
	maybeGC()
	return calleeFormula([]uint64{uint64(a), uint64(b), uint64(c)})
}

var (
	targetsOnce sync.Once
	targets     Targets
	targetsErr  error
	keep        []*execution.Code
)

func buildNativeLeaf(arity int) (uintptr, error) {
	b := n.NewBuilder()
	var args []n.Value
	for i := 0; i < arity; i++ {
		args = append(args, b.CastUnsigned(b.ParamNative(), 64))
	}
	r := b.Const(0x1234567)
	k := []uint64{0x9E37, 0x85EB, 0xC2B2}
	for i, a := range args {
		r = b.Add(r, b.Mul(a, b.Const(k[i])))
	}
	// busy work to clobber many registers
	var xs []n.Value
	for i := 0; i < 20; i++ {
		xs = append(xs, b.Add(b.Mul(args[0], b.Const(uint64(i+3))), args[len(args)-1]))
	}
	s1, s2 := xs[0], xs[len(xs)-1]
	for i := 1; i < len(xs); i++ {
		s1 = b.Xor(s1, xs[i])
		s2 = b.Xor(s2, xs[len(xs)-1-i])
	}
	r = b.Xor(r, b.Xor(s1, s2))
	b.Return(r)
	p, err := b.Finish()
	if err != nil {
		return 0, err
	}
	c, err := n.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
	if err != nil {
		return 0, err
	}
	keep = append(keep, c)
	addr, err := c.EntryAddress()
	if err != nil {
		return 0, err
	}
	return addr, nil
}

func getTargets() (*Targets, error) {
	targetsOnce.Do(func() {
		fns := []any{nil, goCallee1, goCallee2, goCallee3}
		for i := 1; i <= 3; i++ {
			targets.goFn[i] = reflect.ValueOf(fns[i]).Pointer()
			a, err := execution.RegisterForeignCallback(fns[i])
			if err != nil {
				targetsErr = err
				return
			}
			targets.cbFn[i] = a
			if targets.nativeC[i], err = buildNativeLeaf(i); err != nil {
				targetsErr = err
				return
			}
		}
	})
	return &targets, targetsErr
}

type Config struct {
	name  string
	opt   compile.Func // Context selects native, Go-managed or foreign entry
	fn    bool
	errOK bool // errors tolerated (stack requirement)
	// tier is the compile tier; TierBaseline configurations skip what the
	// tier does not implement yet (compile.ErrUnsupported).
	tier compile.Tier
}

// baselineTwin returns c compiled at TierBaseline.
func baselineTwin(c Config) Config {
	c.name, c.tier = "Baseline/"+c.name, compile.TierBaseline
	return c
}

func configs(p *Program) []Config {
	plat := func(f abi.Flavor) compile.Func {
		return compile.Func{ABI: f, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}, AssumePointerFree: true}
	}
	goManaged := func(f abi.Flavor) compile.Func {
		o := plat(f)
		o.Context = compile.ContextGoManaged
		return o
	}
	var cs []Config
	if !p.hasCall {
		cs = append(cs,
			Config{name: "Compile/C/Platform", opt: plat(abi.C)},
			Config{name: "Compile/C/NoUse", opt: compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackNoUse}}, errOK: true},
			Config{name: "Compile/Go/Platform+Func", opt: plat(abi.Go), fn: true},
			Config{name: "Compile/Go/NoUse", opt: compile.Func{ABI: abi.Go, Stack: compile.StackPolicy{Practice: abi.StackNoUse}}, errOK: true},
		)
		o := plat(abi.C)
		o.PreferCompactLoops = true
		cs = append(cs, Config{name: "Compile/C/Platform/Compact", opt: o})
		o = plat(abi.C)
		o.Stack.BytesAvailable = 128
		cs = append(cs, Config{name: "Compile/C/Platform128", opt: o, errOK: true})
		cs = append(cs, Config{name: "Go/Go/Platform+Func", opt: goManaged(abi.Go), fn: true}, Config{name: "Go/C/Platform", opt: goManaged(abi.C)})
		return cs
	}
	if !p.goCalls {
		foreign := plat(abi.C)
		foreign.Context = compile.ContextForeign
		cs = append(cs, Config{name: "Foreign/C/Platform", opt: foreign})
	}
	o := goManaged(abi.Go)
	o.Stack.BytesAvailable = 4096
	cs = append(cs,
		Config{name: "Go/Go/Platform+Func", opt: o, fn: true},
		Config{name: "Go/C/Platform", opt: goManaged(abi.C)},
	)
	return cs
}

func finish(p *Program) (*ir.SealedProgram, string, error) {
	tg, err := getTargets()
	if err != nil {
		return nil, "", err
	}
	b, src, emErr := Emit(p, tg)
	if emErr != "" {
		return nil, src, fmt.Errorf("emit: %s", emErr)
	}
	prog, err := b.Finish()
	return prog, src, err
}

type outcome struct {
	got     uint64
	err     error
	skipped bool
	reason  string // exact compile diagnostic for an explicitly tolerated limit
	gap     bool   // the baseline tier does not implement the program yet
}

func runConfig(prog ir.Input, c Config, params [3]uint64) (o outcome) {
	defer func() {
		if r := recover(); r != nil {
			o.err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
	}()
	code, err := n.Compile(prog, compile.TargetSpec{Tier: c.tier}, c.opt)
	if err != nil {
		if c.tier == compile.TierBaseline && errors.Is(err, compile.ErrUnsupported) {
			return outcome{skipped: true, gap: true, reason: err.Error()}
		}
		if (errors.Is(err, compile.ErrBackendLimit) && strings.Contains(err.Error(), "call frame") && strings.Contains(err.Error(), "byte stack limit")) || (c.errOK && errors.Is(err, compile.ErrStackRequired)) || (c.opt.Context == compile.ContextGoManaged && (managedFrameLimit(err) || managedUnsupported(err))) {
			return outcome{skipped: true, reason: err.Error()}
		}
		return outcome{err: fmt.Errorf("compile: %w", err)}
	}
	// Go-registered code is never unmapped; only close leaf code.
	if c.opt.Context == compile.ContextNative {
		defer code.Close()
	}
	lo, hi, err := code.CallPair(execution.Words(params[0], uint32(params[1]), uintptr(params[2]))...)
	if err != nil {
		return outcome{err: fmt.Errorf("call: %w", err)}
	}
	got := execution.Wide(lo, hi)
	if c.fn {
		var f func(uint64, uint32, uintptr) uint64
		err := code.Func(&f)
		if err != nil {
			return outcome{err: fmt.Errorf("Func: %w", err)}
		}
		g2 := f(params[0], uint32(params[1]), uintptr(params[2]))
		if g2 != got {
			return outcome{got: g2, err: fmt.Errorf("Func result %#x differs from Call result %#x", g2, got)}
		}
	}
	return outcome{got: got}
}

var crossArches = []string{"amd64", "arm64", "riscv64", "ppc64le"}

func crossCheck(prog ir.Input, p *Program) (string, error) {
	for _, a := range crossArches {
		for _, f := range []abi.Flavor{abi.C, abi.Go} {
			context := compile.ContextNative
			if p.hasCall {
				context = compile.ContextExternal
			}
			err := func() (err error) {
				defer func() {
					if r := recover(); r != nil {
						err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
					}
				}()
				_, err = lowerListing(prog, compile.TargetSpec{Architecture: a}, compile.Func{ABI: f, Context: context, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}})
				return err
			}()
			if err != nil {
				return fmt.Sprintf("%s/%v", a, f), err
			}
		}
	}
	return "", nil
}

func paramsFor(seed int64) [3]uint64 {
	vals := []uint64{0, 1, 5, 0xffffffffffffffff, 0x8000000000000000, 0x123456789abcdef0, 0x7f, 0xfffffffe}
	s := uint64(seed)*0x9E3779B97F4A7C15 + 1
	var p [3]uint64
	for i := range p {
		s ^= s >> 29
		s *= 0xBF58476D1CE4E5B9
		p[i] = vals[s%uint64(len(vals))] ^ (s >> 40 * uint64(i&1))
	}
	p[1] = uint64(uint32(p[1]))
	p[2] = uint64(uintptr(p[2]))
	return p
}

// failure reports whether p fails under c with the given params.
func failure(p *Program, c Config, params [3]uint64) (bool, string) {
	want, bad := Interpret(p, params)
	if bad != "" {
		return false, ""
	}
	prog, _, err := finish(p)
	if err != nil {
		return false, ""
	}
	o := runConfig(prog, c, params)
	if o.skipped {
		return false, ""
	}
	if o.err != nil {
		return true, "error: " + firstLine(o.err.Error())
	}
	if o.got != want {
		return true, fmt.Sprintf("got %#x want %#x", o.got, want)
	}
	return false, ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func envInt(k string, d int64) int64 {
	if v := os.Getenv(k); v != "" {
		x, _ := strconv.ParseInt(v, 0, 64)
		return x
	}
	return d
}
