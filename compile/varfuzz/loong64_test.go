//go:build linux && loong64

package varfuzz

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
)

// Additional L1 leaf regressions enforce all four ABI/stack configurations
// without compile-limit exemptions. The generic sweep covers integer/FP calls.
func runLoongInteger(t *testing.T, p *Program, index int64) bool {
	t.Helper()
	params := [3]uint64{uint64(index) * 0x8123456789abcdef, uint64(uint32(index)), uint64(index) ^ 0x88776655}
	want, bad := Interpret(p, params)
	if bad != "" {
		t.Logf("seed%d interpreter discard: %s", index, bad)
		return false
	}
	b, src, why := Emit(p, &Targets{})
	if why != "" {
		t.Fatalf("emit %s", why)
	}
	prog, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []abi.Flavor{abi.C, abi.Go} {
		for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
			o := compile.Func{ABI: f, Stack: compile.StackPolicy{Practice: stack}}
			if stack == abi.StackPlatform {
				o.Stack.BytesAvailable = 65536
			}
			c, err := n.Compile(prog, compile.TargetSpec{}, o)
			if err != nil {
				t.Fatalf("seed%d ABI%d stack%d compile%v\n%s", index, f, stack, err, src)
			}
			got, err := c.Call(execution.Words(uintptr(params[0]), uintptr(params[1]), uintptr(params[2]))...)
			c.Close()
			if err != nil || uint64(got) != want {
				t.Fatalf("seed%d ABI%d stack%d got%#x want%#x err%v\n%s", index, f, stack, got, want, err, src)
			}
		}
	}
	return true
}
func TestLoong64LeafIntegerSweep(t *testing.T) {
	count := 16
	if s := os.Getenv("NYANJIT_VAR_SWEEP"); s != "" {
		v, e := strconv.Atoi(s)
		if e != nil || v < 1 {
			t.Fatal("invalid NYANJIT_VAR_SWEEP")
		}
		count = v
	}
	accepted := 0
	for index := int64(0); accepted < count; index++ {
		p := generateBytes(sweepBytes(uint64(index)), index, false, false)
		if _, bad := Interpret(p, [3]uint64{uint64(index) * 0x8123456789abcdef, uint64(uint32(index)), uint64(index) ^ 0x88776655}); bad != "" {
			continue
		}
		runLoongInteger(t, p, index)
		accepted++
	}
	t.Logf("integer programs=%d x 4 ABI/stack configurations=%d", accepted, accepted*4)
}
func TestLoong64LeafIntegerLegacy(t *testing.T) {
	entries, err := legacyCorpus.ReadDir("testdata/legacy")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".gz")
		if !strings.HasSuffix(name, "-false") {
			continue
		}
		data, err := readLegacyProgram(name)
		if err != nil {
			t.Fatal(err)
		}
		var seed int64
		if _, err := fmt.Sscanf(name, "seed-%d-false", &seed); err != nil {
			t.Fatal(err)
		}
		// Replay stored decision bytes with calls disabled; L2 re-enables the original call path.
		p := generateBytes(data, seed, false, false)
		if !runLoongInteger(t, p, seed) {
			t.Fatalf("legacy integer decision stream %s discarded", name)
		}
		count++
	}
	t.Logf("legacy integer programs=%d x 4 configurations (additional strict leaf coverage)", count)
}

// Additional integer call regressions include Go GC/stack growth and callbacks.
func checkLoongIntegerCalls(t *testing.T, p *Program, index int64) map[string]int {
	t.Helper()
	stats := map[string]int{}
	params := paramsFor(index)
	want, bad := Interpret(p, params)
	if bad != "" {
		stats["discard:"+bad]++
		return stats
	}
	prog, src, err := finish(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, cfg := range configs(p) {
		out := runConfig(prog, cfg, params)
		if out.skipped {
			stats["limit:"+cfg.name]++
			continue
		}
		if out.err != nil || out.got != want {
			t.Fatalf("seed%d %s got%#x want%#x err%v\n%s", index, cfg.name, out.got, want, out.err, src)
		}
		stats["pass:"+cfg.name]++
	}
	return stats
}
func TestLoong64IntegerCallLegacy(t *testing.T) {
	entries, err := legacyCorpus.ReadDir("testdata/legacy")
	if err != nil {
		t.Fatal(err)
	}
	totals := map[string]int{}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".gz")
		if !strings.HasSuffix(name, "-false") {
			continue
		}
		data, err := readLegacyProgram(name)
		if err != nil {
			t.Fatal(err)
		}
		var seed int64
		if _, err := fmt.Sscanf(name, "seed-%d-false", &seed); err != nil {
			t.Fatal(err)
		}
		stats := checkLoongIntegerCalls(t, generateBytes(data, seed, false, true), seed)
		for k, v := range stats {
			if strings.HasPrefix(k, "discard:") {
				t.Fatalf("legacy discarded:%s", k)
			}
			totals[k] += v
		}
	}
	t.Logf("original integer legacy call paths: %v", totals)
}
func TestLoong64IntegerCallsSweep(t *testing.T) {
	count := int(envInt("NYANJIT_LOONG_CALL_SWEEP", 16))
	if count < 1 {
		t.Fatal("NYANJIT_LOONG_CALL_SWEEP must be positive")
	}
	totals := map[string]int{}
	for accepted, index := 0, int64(0); accepted < count; index++ {
		p := generateBytes(sweepBytes(uint64(index)), index, false, true)
		stats := checkLoongIntegerCalls(t, p, index)
		discard := false
		for k, v := range stats {
			totals[k] += v
			discard = discard || strings.HasPrefix(k, "discard:")
		}
		if !discard {
			accepted++
		}
	}
	t.Logf("integer call programs=%d: %v", count, totals)
}
