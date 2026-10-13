//go:build linux && arm64

package varfuzz

// Differential listing harness (reviewer-owned, identical in pre and post).
// ZZDIFF_OUT=file ZZDIFF_GEN=varfuzz|rand|work ZZDIFF_FROM=a ZZDIFF_TO=b go test -run TestZZDiff

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// zzOpt is one configuration: the target and the function description.
// Non-managed configs use ContextExternal, which does no callable checks (as
// the old EmitAssembly/Emit/Analyze did not); managed ones use
// ContextGoManaged (the old ManagedCalls).
type zzOpt struct {
	spec compile.TargetSpec
	fn   compile.Func
}

type zzCfg struct {
	name string
	opt  zzOpt
}

// zzUnit lowers p for o with the given call context.
func zzUnit(p ir.Input, o zzOpt, context compile.Context) (*compile.Unit, error) {
	t, err := compile.NewTarget(o.spec)
	if err != nil {
		return nil, err
	}
	f := o.fn
	f.Context = context
	return compile.Lower(t, p, f)
}

// zzListing is the old EmitAssembly: the symbolic-gateway listing.
func zzListing(p ir.Input, o zzOpt) (string, error) {
	u, err := zzUnit(p, o, o.fn.Context)
	if err != nil {
		return "", err
	}
	return u.ListingSymbolic()
}

// zzReport is the old Analyze/Preflight.
func zzReport(p ir.Input, o zzOpt) (abi.EmissionReport, error) {
	u, err := zzUnit(p, o, o.fn.Context)
	if err != nil {
		return abi.EmissionReport{}, err
	}
	return u.Report().Snapshot(), nil
}

// zzTemplate lowers p for o with the given context and encodes its template.
func zzTemplate(p ir.Input, o zzOpt, context compile.Context) (*execution.Template, error) {
	u, err := zzUnit(p, o, context)
	if err != nil {
		return nil, err
	}
	return u.Template()
}

// zzEmit is the old Emit: linked bytes without a base, and the report.
func zzEmit(p ir.Input, o zzOpt) ([]byte, abi.EmissionReport, error) {
	tp, err := zzTemplate(p, o, o.fn.Context)
	if err != nil {
		return nil, abi.EmissionReport{}, err
	}
	code, err := tp.Bytes(nil)
	if err != nil {
		return nil, abi.EmissionReport{}, err
	}
	return code, tp.Report().Snapshot(), nil
}

func zzConfigs(arch string, wide bool) []zzCfg {
	plat := func(f abi.Flavor) zzOpt {
		return zzOpt{compile.TargetSpec{Architecture: arch}, compile.Func{ABI: f, Context: compile.ContextExternal, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}, AssumePointerFree: true}}
	}
	noUse := func(f abi.Flavor) zzOpt {
		return zzOpt{compile.TargetSpec{Architecture: arch}, compile.Func{ABI: f, Context: compile.ContextExternal, Stack: compile.StackPolicy{Practice: abi.StackNoUse}}}
	}
	cs := []zzCfg{
		{"C/Plat", plat(abi.C)},
		{"Go/Plat", plat(abi.Go)},
		{"C/NoUse", noUse(abi.C)},
		{"Go/NoUse", noUse(abi.Go)},
	}
	if !wide {
		return cs
	}
	o := plat(abi.Go)
	o.fn.Context = compile.ContextGoManaged
	cs = append(cs, zzCfg{"Go/Managed", o})
	o = plat(abi.C)
	o.fn.Context = compile.ContextGoManaged
	cs = append(cs, zzCfg{"C/Managed", o})
	o = plat(abi.C)
	o.fn.PreferCompactLoops = true
	cs = append(cs, zzCfg{"C/Compact", o})
	o = plat(abi.C)
	o.fn.Stack.BytesAvailable = 128
	cs = append(cs, zzCfg{"C/Plat128", o})
	o = plat(abi.C)
	o.spec.Vectors = compile.VectorsOff()
	cs = append(cs, zzCfg{"C/NoSIMD", o})
	cpu := func(name, level string, ext ...string) {
		o := plat(abi.C)
		o.spec.CPU = abi.CPUProfile{Level: level, Extensions: ext}
		cs = append(cs, zzCfg{"C/cpu-" + name, o})
		o = noUse(abi.Go)
		o.spec.CPU = abi.CPUProfile{Level: level, Extensions: ext}
		cs = append(cs, zzCfg{"GoNoUse/cpu-" + name, o})
	}
	switch arch {
	case "amd64":
		cpu("v2", "x86-64-v2")
		cpu("v3", "x86-64-v3")
		cpu("v4", "x86-64-v4")
		cpu("v1clmul", "x86-64-v1", "pclmulqdq", "popcnt")
		cpu("v1BMI", "x86-64-v1", "BMI1", "lzcnt")
	case "386":
		cpu("bmi", "i686", "bmi1", "bmi2", "lzcnt", "popcnt")
		cpu("clmul", "i686", "pclmulqdq")
	case "arm64":
		cpu("lse", "armv8.0-a", "lse")
		cpu("sve2", "armv8.0-a", "sve2")
	case "riscv64":
		cpu("rva22", "rva22u64")
		cpu("rva23", "rva23u64")
		cpu("zbb", "rv64gc", "zbb", "zba", "zicond")
	case "ppc64le":
		cpu("p9", "power9")
		cpu("p10", "power10")
		// An invalid POWER level (the old PPC64CPU(2) probe): must be rejected.
		o := plat(abi.C)
		o.spec.CPU = abi.CPUProfile{Level: "power2"}
		cs = append(cs, zzCfg{"C/ppc64cpu2", o})
	case "loong64":
		cpu("lsx", "la64v1.0", "lsx")
		cpu("lasx", "la64v1.0", "lasx")
	}
	return cs
}

var zzArches = []string{"amd64", "arm64", "386", "riscv64", "ppc64le", "loong64"}

func zzEnv(k string, d int64) int64 {
	if v := os.Getenv(k); v != "" {
		x, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			return x
		}
	}
	return d
}

// zzBuild returns a builder for (gen, seed) or nil.
func zzBuild(gen string, seed int64) (b *n.Builder, err error) {
	defer func() {
		if r := recover(); r != nil {
			b, err = nil, fmt.Errorf("gen panic: %v", r)
		}
	}()
	switch gen {
	case "varfuzz":
		tg := &Targets{goFn: [4]uintptr{0, 0x41000, 0x42000, 0x43000}, cbFn: [4]uintptr{0, 0x51000, 0x52000, 0x53000}, nativeC: [4]uintptr{0, 0x61000, 0x62000, 0x63000}}
		p := Generate(seed, seed%2 == 0, seed%3 != 0)
		bb, _, e := Emit(p, tg)
		if e != "" {
			return nil, fmt.Errorf("emit: %s", e)
		}
		return bb, nil
	case "rand":
		return zzRandom(seed), nil
	case "work":
		return zzWork(seed), nil
	case "scope":
		return zzScopeGen(seed), nil
	case "pscope":
		return zzPScopeGen(seed), nil
	}
	return nil, fmt.Errorf("bad gen")
}

func TestZZDiff(t *testing.T) {
	out := os.Getenv("ZZDIFF_OUT")
	if out == "" {
		t.Skip()
	}
	gen := os.Getenv("ZZDIFF_GEN")
	from, to := zzEnv("ZZDIFF_FROM", 0), zzEnv("ZZDIFF_TO", 10)
	wide := os.Getenv("ZZDIFF_WIDE") != ""
	dump := os.Getenv("ZZDIFF_DUMP") // "seed/arch/cfg" -> write listing
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()
	type res struct {
		seed  int64
		lines []string
	}
	results := make(chan res, 64)
	seeds := make(chan int64)
	var wg sync.WaitGroup
	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for seed := range seeds {
				results <- res{seed, zzOne(gen, seed, wide, dump)}
			}
		}()
	}
	go func() {
		for s := from; s < to; s++ {
			seeds <- s
		}
		close(seeds)
		wg.Wait()
		close(results)
	}()
	all := []res{}
	for r := range results {
		all = append(all, r)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].seed < all[j].seed })
	count := 0
	for _, r := range all {
		for _, l := range r.lines {
			fmt.Fprintln(w, l)
			count++
		}
	}
	t.Logf("%d lines", count)
}

func zzOne(gen string, seed int64, wide bool, dump string) []string {
	var lines []string
	b, err := zzBuild(gen, seed)
	if err != nil {
		return []string{fmt.Sprintf("%s %d - - BUILD %s", gen, seed, zzFirst(err.Error()))}
	}
	prog, err := b.Finish()
	if err != nil {
		return []string{fmt.Sprintf("%s %d - - FINISH %s", gen, seed, zzFirst(err.Error()))}
	}
	for _, arch := range zzArches {
		for _, c := range zzConfigs(arch, wide) {
			key := fmt.Sprintf("%s %d %s %s", gen, seed, arch, c.name)
			listing, err := func() (s string, err error) {
				defer func() {
					if r := recover(); r != nil {
						err = fmt.Errorf("panic: %v %s", r, zzFirst(string(debug.Stack())))
					}
				}()
				return zzListing(prog, c.opt)
			}()
			if dump != "" && dump == fmt.Sprintf("%d/%s/%s", seed, arch, c.name) {
				os.WriteFile(os.Getenv("ZZDIFF_DUMPFILE"), []byte(listing+"\nERR:"+fmt.Sprint(err)), 0644)
			}
			if err != nil {
				lines = append(lines, key+" ERR "+zzFirst(err.Error()))
				continue
			}
			d := sha256.Sum256([]byte(listing))
			lines = append(lines, key+" OK "+hex.EncodeToString(d[:8])+" "+strconv.Itoa(len(listing)))
		}
		if arch == runtime.GOARCH {
			// Native bytes + report on the host target.
			for _, c := range zzConfigs(arch, false) {
				key := fmt.Sprintf("%s %d %s %s", gen, seed, arch, "native-"+c.name)
				code, rep, err := func() (code []byte, rep abi.EmissionReport, err error) {
					defer func() {
						if r := recover(); r != nil {
							err = fmt.Errorf("panic: %v", r)
						}
					}()
					return zzEmit(prog, c.opt)
				}()
				if err != nil {
					lines = append(lines, key+" ERR "+zzFirst(err.Error()))
					continue
				}
				d := sha256.Sum256(code)
				an, aerr := zzReport(prog, c.opt)
				lines = append(lines, fmt.Sprintf("%s OK %s code=%d rep.code=%d an.code=%d an.err=%v stack=%d ws=%d flags=%v cpu=%v", key, hex.EncodeToString(d[:8]), len(code), rep.CodeBytes, an.CodeBytes, aerr != nil, rep.StackBytes, rep.WorkspaceBytes, rep.FlagsClobbered, rep.CPU.Extensions))
			}
		}
	}
	return lines
}

func zzFirst(s string) string {
	s = strings.ReplaceAll(s, "\n", " | ")
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// ---------------- deterministic workloads

func zzWork(seed int64) *n.Builder {
	size := []int{3, 8, 20, 60, 150}[seed%5]
	switch (seed / 5) % 7 {
	case 0:
		b := n.NewBuilder()
		x, y := b.Param(), b.Param()
		acc := make([]n.Value, 8)
		for i := range acc {
			acc[i] = b.Add(x, b.Const(uint64(i+1)))
		}
		for i := 0; i < size; i++ {
			j := i % 8
			switch i % 3 {
			case 0:
				acc[j] = b.Add(acc[j], y)
			case 1:
				acc[j] = b.Xor(acc[j], acc[(j+1)%8])
			default:
				acc[j] = b.RotateLeft(acc[j], 7)
			}
		}
		s := acc[0]
		for _, a := range acc[1:] {
			s = b.Add(s, a)
		}
		b.Return(s)
		return b
	case 1:
		b := n.NewBuilder()
		x, y := b.Param(), b.Param()
		vals := []n.Value{}
		v := x
		for i := 0; i < size; i++ {
			v = b.Add(b.Mul(v, y), b.Const(uint64(i)))
			vals = append(vals, v)
		}
		s := vals[0]
		for _, a := range vals[1:] {
			s = b.Xor(s, a)
		}
		b.Return(s)
		return b
	case 2:
		b := n.NewBuilder()
		f := b.Float64(1.5)
		acc := make([]n.Value, 8)
		for i := range acc {
			acc[i] = b.FAdd(f, b.Float64(float64(i)))
		}
		for i := 0; i < size; i++ {
			j := i % 8
			if i%2 == 0 {
				acc[j] = b.FMul(acc[j], acc[(j+3)%8])
			} else {
				acc[j] = b.FAdd(acc[j], b.Float64(0.25))
			}
		}
		s := acc[0]
		for _, a := range acc[1:] {
			s = b.FAdd(s, a)
		}
		b.Return(s)
		return b
	case 3:
		b := n.NewBuilder()
		x, y := b.Param(), b.Param()
		acc := b.Var(x)
		yv := b.Var(y)
		for i := 0; i < size/3+1; i++ {
			b.Enter()
			k := b.Var(b.Const(3))
			head, body, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
			b.Jump(head)
			b.Bind(head)
			b.Branch(b.CompareImmediate(b.Get(k), ir.BranchEqual, 0), done)
			b.Jump(body)
			b.Bind(body)
			b.Set(acc, b.Xor(b.Add(b.Get(acc), b.Get(yv)), b.Const(uint64(i))))
			b.Set(k, b.Sub(b.Get(k), b.Const(1)))
			b.Jump(head)
			b.Bind(done)
			b.Leave()
		}
		b.Return(b.Get(acc))
		return b
	case 4:
		b := n.NewBuilder()
		x := b.Param()
		f := b.FAdd(x, b.Float64(1.5))
		for i := 0; i < size/2+1; i++ {
			f = b.FAdd(f, b.Float64(0.25))
			x = b.Xor(x, b.CallC(b.ConstNative(0x1234), x))
		}
		b.Return(b.Xor(x, f))
		return b
	case 5:
		b := n.NewBuilder()
		ptr := b.ParamNative()
		typ := ir.Vec(4, 32)
		seedv := b.VLoad(typ, ptr)
		v := seedv
		for i := 0; i < size; i++ {
			v = b.Xor(b.RotateRight(v, 7), b.Add(v, seedv))
		}
		b.VStore(ptr, v)
		b.Return(b.ConstNative(0))
		return b
	default:
		b := n.NewBuilder()
		v := b.Var(b.Param())
		for i := 0; i < size; i++ {
			b.Enter()
			b.Set(v, b.Add(b.Get(v), b.Const(uint64(i+1))))
		}
		for i := 0; i < size; i++ {
			b.Set(v, b.Xor(b.Get(v), b.Const(uint64(i+1))))
			b.Leave()
		}
		b.Return(b.Get(v))
		return b
	}
}

// ---------------- random straight-line + structured control generator

type zzG struct {
	r      *rand.Rand
	b      *n.Builder
	i64    []n.Value
	i32    []n.Value
	f64    []n.Value
	f32    []n.Value
	vec    map[string][]n.Value
	vt     map[string]ir.Type
	vars   []n.Var
	fvar   []n.Var
	ptr    n.Value
	hasPtr bool
	ptrVar n.Var
	fp     bool
	vecs   bool
	call   bool
}

func (g *zzG) pick(s []n.Value) n.Value { return s[g.r.Intn(len(s))] }

func (g *zzG) reset() {
	// After a label, only Vars and fresh values are used.
	g.i64 = g.i64[:0]
	g.i32 = g.i32[:0]
	g.f64 = g.f64[:0]
	g.f32 = g.f32[:0]
	for k := range g.vec {
		g.vec[k] = nil
	}
	for _, v := range g.vars {
		g.i64 = append(g.i64, g.b.Get(v))
	}
	for _, v := range g.fvar {
		g.f64 = append(g.f64, g.b.Get(v))
	}
	g.i64 = append(g.i64, g.b.Const(uint64(g.r.Int63())))
	g.i32 = append(g.i32, g.b.CastUnsigned(g.pick(g.i64), 32))
	if g.fp && len(g.f64) == 0 {
		g.f64 = append(g.f64, g.b.Float64(g.r.Float64()))
	}
	g.hasPtr = true
	g.ptr = g.b.Get(g.ptrVar)
}

func (g *zzG) op() {
	b, r := g.b, g.r
	switch k := r.Intn(30); {
	case k < 8:
		x, y := g.pick(g.i64), g.pick(g.i64)
		var v n.Value
		switch r.Intn(14) {
		case 0:
			v = b.Add(x, y)
		case 1:
			v = b.Sub(x, y)
		case 2:
			v = b.Mul(x, y)
		case 3:
			v = b.And(x, y)
		case 4:
			v = b.Or(x, y)
		case 5:
			v = b.Xor(x, y)
		case 6:
			v = b.ShiftLeft(x, uint8(r.Intn(63)+1))
		case 7:
			v = b.ShiftRightArithmetic(x, uint8(r.Intn(63)+1))
		case 8:
			v = b.RotateRightBy(x, y)
		case 9:
			v = b.ShiftRightBy(x, y)
		case 10:
			v = b.Add(x, b.Const(uint64(r.Int63n(1<<uint(r.Intn(40)+1)))))
		case 11:
			v = b.LeadingZeros(x)
		case 12:
			v = b.TrailingZeros(x)
		default:
			v = b.Select(b.And(x, b.Const(1)), y, x)
		}
		g.i64 = append(g.i64, v)
	case k < 11:
		x, y := g.pick(g.i32), g.pick(g.i32)
		var v n.Value
		switch r.Intn(5) {
		case 0:
			v = b.Add(x, y)
		case 1:
			v = b.Mul(x, y)
		case 2:
			v = b.Xor(x, b.ConstBits(32, uint64(r.Uint32())))
		case 3:
			v = b.RotateLeft(x, uint8(r.Intn(31)+1))
		default:
			v = b.Sub(x, y)
		}
		g.i32 = append(g.i32, v)
	case k < 13:
		if r.Intn(2) == 0 {
			g.i32 = append(g.i32, b.Xor(g.pick(g.i32), b.ConstBits(32, 5)))
		} else {
			g.i32 = append(g.i32, b.CastUnsigned(g.pick(g.i64), 32))
		}
	case k < 18 && g.fp:
		if r.Intn(4) == 0 && len(g.f32) > 0 {
			x, y := g.pick(g.f32), g.pick(g.f32)
			switch r.Intn(4) {
			case 0:
				g.f32 = append(g.f32, b.FAdd(x, y))
			case 1:
				g.f32 = append(g.f32, b.FMul(x, b.Float32(float32(r.Float64()))))
			case 2:
				g.f64 = append(g.f64, b.FConvert(x, 64))
			default:
				g.f32 = append(g.f32, b.FSqrt(x))
			}
			return
		}
		x, y := g.pick(g.f64), g.pick(g.f64)
		switch r.Intn(9) {
		case 0:
			g.f64 = append(g.f64, b.FAdd(x, y))
		case 1:
			g.f64 = append(g.f64, b.FSub(x, b.Float64(r.Float64()*100)))
		case 2:
			g.f64 = append(g.f64, b.FMul(x, y))
		case 3:
			g.f64 = append(g.f64, b.FDiv(x, y))
		case 4:
			g.f64 = append(g.f64, b.FMA(x, y, g.pick(g.f64)))
		case 5:
			g.f32 = append(g.f32, b.FConvert(x, 32))
		case 6:
			g.f64 = append(g.f64, b.FSqrt(x))
		case 7:
			g.i64 = append(g.i64, b.Xor(x, g.pick(g.i64))) // bit reinterpretation
		default:
			g.f64 = append(g.f64, b.FAdd(x, b.Float64(0)))
		}
	case k < 22 && g.vecs:
		names := []string{"4x32", "2x64", "16x8", "8x16"}
		name := names[r.Intn(len(names))]
		t := g.vt[name]
		if len(g.vec[name]) == 0 || r.Intn(5) == 0 {
			switch r.Intn(3) {
			case 0:
				if !g.hasPtr {
					g.ptr, g.hasPtr = g.b.ParamNative(), true
				}
				g.vec[name] = append(g.vec[name], b.VLoad(t, g.ptr))
			case 1:
				g.vec[name] = append(g.vec[name], b.Splat(t, b.CastUnsigned(g.pick(g.i64), t.LaneBits)))
			default:
				data := make([]byte, 16)
				r.Read(data)
				g.vec[name] = append(g.vec[name], b.ConstVector(t, data))
			}
			return
		}
		x, y := g.pick(g.vec[name]), g.pick(g.vec[name])
		switch r.Intn(9) {
		case 0:
			g.vec[name] = append(g.vec[name], b.Add(x, y))
		case 1:
			g.vec[name] = append(g.vec[name], b.Xor(x, y))
		case 2:
			g.vec[name] = append(g.vec[name], b.RotateRight(x, uint8(r.Intn(int(t.LaneBits)-1)+1)))
		case 3:
			g.vec[name] = append(g.vec[name], b.Sub(x, y))
		case 4:
			b.StoreBits(g.ptr, b.ExtractLane(x, uint16(r.Intn(int(t.Lanes)))))
		case 5:
			g.vec[name] = append(g.vec[name], b.InsertLane(x, uint16(r.Intn(int(t.Lanes))), b.CastUnsigned(g.pick(g.i64), t.LaneBits)))
		case 6:
			b.StoreBits(g.ptr, b.MaskBits(b.VCompare(x, ir.BranchEqual, y)))
		case 7:
			if name == "2x64" {
				g.vec[name] = append(g.vec[name], b.CarrylessMul(x, y, 0, 1))
			} else {
				g.vec[name] = append(g.vec[name], b.And(x, y))
			}
		default:
			if !g.hasPtr {
				g.ptr, g.hasPtr = g.b.ParamNative(), true
			}
			b.VStore(g.ptr, x)
		}
	case k < 24:
		if !g.hasPtr {
			g.ptr, g.hasPtr = b.ParamNative(), true
			g.ptrVar = b.Var(g.ptr)
		}
		switch r.Intn(4) {
		case 0:
			g.i64 = append(g.i64, b.Load64(g.ptr))
		case 1:
			b.Store32(g.ptr, g.pick(g.i32))
		case 2:
			b.Store8(g.ptr, b.Load8(g.ptr))
		default:
			b.Store64(g.ptr, g.pick(g.i64))
		}
	case k < 25 && g.call:
		args := []n.Value{}
		for i := r.Intn(4); i > 0; i-- {
			args = append(args, g.pick(g.i64))
		}
		if r.Intn(2) == 0 {
			g.i64 = append(g.i64, b.CallC(b.ConstNative(0x7000), args...))
		} else {
			g.i64 = append(g.i64, b.CallGo(b.ConstNative(0x8000), args...))
		}
	case k < 27 && len(g.vars) > 0:
		v := g.vars[r.Intn(len(g.vars))]
		b.Set(v, g.pick(g.i64))
		g.i64 = append(g.i64, b.Get(v))
	default:
		g.i64 = append(g.i64, b.Const(uint64(r.Int63n(1<<uint(r.Intn(62)+1)))))
	}
}

func (g *zzG) block(depth, ops int) {
	for i := 0; i < ops; i++ {
		g.op()
	}
	if depth > 3 {
		return
	}
	switch g.r.Intn(6) {
	case 0: // loop
		b := g.b
		b.Enter()
		k := b.Var(b.Const(uint64(g.r.Intn(5) + 1)))
		head, body, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Jump(head)
		b.Bind(head)
		g.reset()
		b.Branch(b.CompareImmediate(b.Get(k), ir.BranchEqual, 0), done)
		b.Jump(body)
		b.Bind(body)
		g.reset()
		g.block(depth+1, g.r.Intn(12))
		if len(g.vars) > 0 {
			b.Set(g.vars[0], b.Xor(b.Get(g.vars[0]), g.pick(g.i64)))
		}
		if len(g.fvar) > 0 && len(g.f64) > 0 {
			b.Set(g.fvar[0], g.pick(g.f64))
		}
		b.Set(k, b.Sub(b.Get(k), b.Const(1)))
		b.Jump(head)
		b.Bind(done)
		b.Leave()
		g.reset()
	case 1: // diamond
		b := g.b
		t, e, j := b.NewLabel(), b.NewLabel(), b.NewLabel()
		conds := []ir.BranchCondition{ir.BranchEqual, ir.BranchNotEqual, ir.BranchLess, ir.BranchGreaterEqual, ir.BranchBelow, ir.BranchAbove}
		var pred n.Predicate
		switch g.r.Intn(3) {
		case 0:
			pred = b.Compare(g.pick(g.i64), conds[g.r.Intn(len(conds))], g.pick(g.i64))
		case 1:
			pred = b.IsZero(g.pick(g.i64))
		default:
			pred = b.CompareImmediate(g.pick(g.i64), conds[g.r.Intn(len(conds))], int64(g.r.Intn(100)))
		}
		b.Branch(pred, t)
		b.Jump(e)
		b.Bind(t)
		g.reset()
		g.block(depth+1, g.r.Intn(8))
		if len(g.vars) > 0 {
			b.Set(g.vars[g.r.Intn(len(g.vars))], g.pick(g.i64))
		}
		b.Jump(j)
		b.Bind(e)
		g.reset()
		g.block(depth+1, g.r.Intn(8))
		if len(g.vars) > 0 {
			b.Set(g.vars[g.r.Intn(len(g.vars))], g.pick(g.i64))
		}
		b.Jump(j)
		b.Bind(j)
		g.reset()
	case 2: // scope with a new var
		b := g.b
		b.Enter()
		v := b.Var(g.pick(g.i64))
		g.vars = append(g.vars, v)
		g.block(depth+1, g.r.Intn(10))
		g.vars = g.vars[:len(g.vars)-1]
		b.Leave()
		g.reset()
	}
}

func zzRandom(seed int64) *n.Builder {
	r := rand.New(rand.NewSource(seed*7919 + 17))
	b := n.NewBuilder()
	g := &zzG{r: r, b: b, vec: map[string][]n.Value{}, vt: map[string]ir.Type{
		"4x32": ir.Vec(4, 32), "2x64": ir.Vec(2, 64), "16x8": ir.Vec(16, 8), "8x16": ir.Vec(8, 16)}}
	mode := seed % 8
	g.fp = mode&1 != 0
	g.vecs = mode&2 != 0
	g.call = mode&4 != 0
	x, y := b.Param(), b.ParamBits(32)
	g.ptr, g.hasPtr = b.ParamNative(), true
	g.ptrVar = b.Var(g.ptr)
	g.i64 = append(g.i64, x)
	g.i32 = append(g.i32, y)
	if g.fp {
		g.f64 = append(g.f64, b.FAdd(x, b.Float64(1.25)))
		g.f32 = append(g.f32, b.Float32(2.5))
	}
	control := r.Intn(3) != 0
	if control {
		for i := r.Intn(4); i >= 0; i-- {
			g.vars = append(g.vars, b.Var(g.pick(g.i64)))
		}
		if g.fp && r.Intn(2) == 0 {
			g.fvar = append(g.fvar, b.Var(g.pick(g.f64)))
		}
	}
	ops := 5 + r.Intn([]int{20, 60, 200}[r.Intn(3)])
	if control {
		for i := 0; i < 1+r.Intn(4); i++ {
			g.block(0, ops/3)
		}
	} else {
		for i := 0; i < ops; i++ {
			g.op()
		}
	}
	// Fold everything live into the result to create pressure.
	s := g.pick(g.i64)
	for _, v := range g.i64 {
		if r.Intn(2) == 0 {
			s = b.Xor(s, v)
		}
	}
	for _, v := range g.i32 {
		if r.Intn(3) == 0 {
			b.Store32(g.ptr, v)
		}
	}
	if g.fp && len(g.f64) > 0 {
		f := g.pick(g.f64)
		for _, v := range g.f64 {
			if r.Intn(2) == 0 {
				f = b.FAdd(f, v)
			}
		}
		if r.Intn(2) == 0 {
			b.Return(f)
			return b
		}
		s = b.Xor(s, f)
	}
	for _, v := range g.vars {
		s = b.Add(s, b.Get(v))
	}
	b.Return(s)
	return b
}
