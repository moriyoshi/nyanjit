//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le || 386)

package varfuzz

import (
	"context"
	"fmt"
	n "github.com/moriyoshi/nyanjit"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// FuzzScopedVars checks scoped mutable variables against one AST interpreter.
// The byte stream controls expression trees, pressure, snapshots, captures,
// exports, branches, bounded loops, breaks, label arguments and outbound calls.
// The remaining arguments select integer/FP generation, calls, compiler/ABI/
// stack configuration and runtime arguments. Go-entry configurations also
// compare execution.Func with Code.Call.
func FuzzScopedVars(f *testing.F) {
	f.Add([]byte{}, false, false, uint8(0), uint64(0), uint32(0), uint64(0))
	f.Fuzz(func(t *testing.T, data []byte, fp, calls bool, config uint8, a uint64, b uint32, c uint64) {
		if fp && runtime.GOARCH == "386" {
			t.Skip("386 FP lowering is unsupported")
		}
		if len(data) > 65536 {
			data = data[:65536]
		}
		p := generateBytes(data, 0, fp, calls)
		params := [3]uint64{a, uint64(b), uint64(uintptr(c))}
		// The compact minimized corpus checks every configuration with one
		// stored program. Mutated arbitrary trees select one configuration.
		all := config == 255 && len(data) >= 4 && string(data[:3]) == "var"
		checkProgram(t, p, params, int(config), all)
	})
}

func checkProgram(t *testing.T, p *Program, params [3]uint64, config int, all bool) map[string]int {
	t.Helper()
	stats := map[string]int{}
	want, bad := Interpret(p, params)
	if bad != "" {
		stats["discard:"+bad]++
		return stats
	}
	prog, src, err := finish(p)
	if err != nil {
		t.Fatalf("builder: %v\n%s", err, src)
	}
	cs := configs(p)
	if !all {
		cs = []Config{cs[config%len(cs)]}
	}
	for _, cfg := range cs {
		out := runConfig(prog, cfg, params)
		if out.skipped {
			stats["limit:"+cfg.name]++
			continue
		}
		if out.err != nil || out.got != want {
			t.Fatalf("sweep index %d %s args=%#x: got %#x want %#x error=%v\n%s", p.seed, cfg.name, params, out.got, want, out.err, src)
		}
		stats["pass:"+cfg.name]++
	}
	return stats
}

// TestScopedVarsSweep is small by default. E2E and acceptance jobs explicitly
// set NYANJIT_VAR_SWEEP; each generated case runs every applicable configuration.
func TestScopedVarsSweep(t *testing.T) {
	count := int(envInt("NYANJIT_VAR_SWEEP", 16))
	start := uint64(envInt("NYANJIT_VAR_START", 0))

	// Registered Go modules cannot be unloaded. Bound process lifetime so long
	// sweeps neither accumulate mappings nor make runtime module scans quadratic.
	if count > 64 && os.Getenv("NYANJIT_VAR_CHILD") != "1" {

		// Limit subprocesses independently of -test.parallel; four batches keep
		// emulated targets busy without unbounded process or metadata growth.
		slots := make(chan struct{}, 4)
		for offset := 0; offset < count; offset += 64 {
			offset := offset
			t.Run(fmt.Sprintf("batch-%d", start+uint64(offset)), func(t *testing.T) {
				t.Parallel()
				slots <- struct{}{}
				defer func() { <-slots }()
				size := min(64, count-offset)
				ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestScopedVarsSweep$", "-test.v", "-test.timeout=10m")
				cmd.Env = append(os.Environ(), "NYANJIT_VAR_CHILD=1", "GOMAXPROCS=1", "NYANJIT_VAR_SWEEP="+strconv.Itoa(size), "NYANJIT_VAR_START="+strconv.FormatUint(start+uint64(offset), 10))
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("sweep batch %d..%d: %v\n%s", start+uint64(offset), start+uint64(offset+size-1), err, output)
				}
				t.Logf("batch %d..%d\n%s", start+uint64(offset), start+uint64(offset+size-1), output)
			})
		}
		return
	}
	for _, fp := range []bool{false, true} {
		if fp && runtime.GOARCH == "386" {
			continue
		}
		t.Run(fmt.Sprintf("fp=%t", fp), func(t *testing.T) {
			stats := map[string]int{}

			for accepted, index := 0, start; accepted < count; index++ {
				data := sweepBytes(index)
				params := paramsFor(int64(index))
				leaf := generateBytes(data, int64(index), fp, false)
				mixed := generateBytes(data, int64(index), fp, true)
				native := generateBytes(data, int64(index), fp, true)
				makeNativeCalls(native)
				programs := []*Program{leaf, native, mixed}
				valid := true
				for _, p := range programs {
					if _, bad := Interpret(p, params); bad != "" {
						stats["discard:"+bad]++
						valid = false
						break
					}
				}
				if !valid {
					continue
				}
				for _, p := range programs {
					for k, v := range checkProgram(t, p, params, 0, true) {
						stats[k] += v
					}
				}
				accepted++
			}
			t.Logf("programs=%d start=%d results=%v", count, start, stats)
		})
	}
}

// TestScopedVarsMinimize is an opt-in, crash-safe AST reducer. The input file
// contains the raw AST decision bytes, not a random seed. Configuration and
// arguments are explicit; output includes a Builder program for a regression.
func TestScopedVarsMinimize(t *testing.T) {
	path := os.Getenv("NYANJIT_VAR_MINIMIZE")
	if path == "" {
		t.Skip("set NYANJIT_VAR_MINIMIZE to a raw byte-stream input")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fp := os.Getenv("NYANJIT_VAR_FP") == "1"
	p := generateBytes(data, 0, fp, true)
	cs := configs(p)
	cfg := cs[int(envInt("NYANJIT_VAR_CONFIG", 0))%len(cs)]
	es := minimizeBytes(data, fp, cfg, paramsFor(0))
	reduced := generateBytes(data, 0, fp, true)
	for _, e := range es {
		apply(reduced, e)
	}
	_, src, err := finish(reduced)
	t.Logf("configuration=%s edits=%s finish=%v\n%s", cfg.name, editsString(es), err, src)
}

// Reuse the same AST/interpreter for a configuration with only native C calls.
// The final call also checks preservation of every root variable home.
func makeNativeCalls(p *Program) {
	var visit func([]*Stmt)
	visit = func(ss []*Stmt) {
		for _, s := range ss {
			if s.kind == "call" {
				s.ck = 2
			}
			visit(s.then)
			visit(s.els)
			visit(s.body)
			if s.scope != nil {
				visit(s.scope.body)
			}
		}
	}
	visit(p.root.body)
	p.ntemps++
	p.root.body = append(p.root.body, &Stmt{kind: "call", ck: 2, t: &Temp{id: p.ntemps, bits: 64}, args: []*Expr{{op: "cast", bits: n.NativeBits, a: &Expr{op: "get", bits: 64, v: p.hash}}}})
	p.hasCall, p.goCalls = true, false
}
