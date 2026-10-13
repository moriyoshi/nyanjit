//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le || 386)

package varfuzz

import (
	"encoding/binary"
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

	"unsafe"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
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
	c, err := compile.Compile(p, abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 4096})
	if err != nil {
		return 0, err
	}
	keep = append(keep, c)
	addr, err := c.EntryAddress()
	if err != nil {
		return 0, err
	}
	if runtime.GOARCH == "ppc64" {
		if conv, _ := abi.DefaultPPC64CABI("ppc64"); conv == abi.PPC64ELFv1 {
			d, err := execution.Scratch(24)
			if err != nil {
				return 0, err
			}
			binary.NativeEndian.PutUint64(d, uint64(addr))
			addr = uintptr(unsafe.Pointer(&d[0]))
		}
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
	mode  int // 0 Compile, 1 CompileGo, 2 CompileForeign
	opt   abi.CompileOptions
	fn    bool
	errOK bool // errors tolerated (stack requirement)
}

func configs(p *Program) []Config {
	plat := func(f abi.Flavor) abi.CompileOptions {
		return abi.CompileOptions{ABI: f, Stack: abi.StackPlatform, StackBytesAvailable: 8192, AssumePointerFree: true}
	}
	var cs []Config
	if !p.hasCall {
		cs = append(cs,
			Config{name: "Compile/C/Platform", opt: plat(abi.C)},
			Config{name: "Compile/C/NoUse", opt: abi.CompileOptions{ABI: abi.C, Stack: abi.StackNoUse}, errOK: true},
			Config{name: "Compile/Go/Platform+Func", opt: plat(abi.Go), fn: true},
			Config{name: "Compile/Go/NoUse", opt: abi.CompileOptions{ABI: abi.Go, Stack: abi.StackNoUse}, errOK: true},
		)
		o := plat(abi.C)
		o.PreferCompactLoops = true
		cs = append(cs, Config{name: "Compile/C/Platform/Compact", opt: o})
		o = plat(abi.C)
		o.StackBytesAvailable = 128
		cs = append(cs, Config{name: "Compile/C/Platform128", opt: o, errOK: true})
		cs = append(cs, Config{name: "Go/Go/Platform+Func", mode: 1, opt: plat(abi.Go), fn: true}, Config{name: "Go/C/Platform", mode: 1, opt: plat(abi.C)})
		return cs
	}
	if !p.goCalls {
		cs = append(cs, Config{name: "Foreign/C/Platform", mode: 2, opt: plat(abi.C)})
	}
	o := plat(abi.Go)
	o.StackBytesAvailable = 4096
	cs = append(cs,
		Config{name: "Go/Go/Platform+Func", mode: 1, opt: o, fn: true},
		Config{name: "Go/C/Platform", mode: 1, opt: plat(abi.C)},
	)
	return cs
}

func finish(p *Program) (*ir.Program, string, error) {
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
}

func runConfig(prog *ir.Program, c Config, params [3]uint64) (o outcome) {
	defer func() {
		if r := recover(); r != nil {
			o.err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
	}()
	var code *execution.Code
	var err error
	switch c.mode {
	case 0:
		code, err = compile.Compile(prog, c.opt)
	case 1:
		code, err = compile.CompileGo(prog, c.opt)
	case 2:
		code, err = compile.CompileForeign(prog, c.opt)
	}
	if err != nil {
		if (c.errOK && errors.Is(err, compile.ErrStackRequired)) || (c.mode == 1 && errors.Is(err, compile.ErrBackendLimit) && strings.Contains(err.Error(), "managed frame needs")) {
			return outcome{skipped: true}
		}
		return outcome{err: fmt.Errorf("compile: %w", err)}
	}
	// Go-registered code is never unmapped; only close leaf code.
	if c.mode == 0 {
		defer code.Close()
	}
	words := execution.Words("Lll", params[0], uint64(uint32(params[1])), uint64(uintptr(params[2])))
	lo, hi, err := code.CallPair(words...)
	if err != nil {
		return outcome{err: fmt.Errorf("call: %w", err)}
	}
	got := execution.Wide(lo, hi)
	if c.fn {
		f, err := execution.Func[func(uint64, uint32, uintptr) uint64](code)
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

var crossArches = []string{"amd64", "arm64", "riscv64", "ppc64le", "ppc64"}

func crossCheck(prog *ir.Program, p *Program) (string, error) {
	for _, a := range crossArches {
		for _, f := range []abi.Flavor{abi.C, abi.Go} {
			err := func() (err error) {
				defer func() {
					if r := recover(); r != nil {
						err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
					}
				}()
				_, err = compile.EmitAssembly(prog, abi.CompileOptions{Architecture: a, ABI: f, Stack: abi.StackPlatform, StackBytesAvailable: 8192})
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
