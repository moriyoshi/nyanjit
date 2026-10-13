//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strconv"
	"testing"
	"time"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestPredicateChains(t *testing.T) {
	if os.Getenv("NYANJIT_PREDICATE_CHAINS_CHILD") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPredicateChains$", "-test.v")
		cmd.Env = append(os.Environ(), "NYANJIT_PREDICATE_CHAINS_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("predicate chains: %v\n%s", err, out)
		}
		return
	}
	debug.SetMaxStack(1 << 20)
	// Exercise long chains inside the program budget. Larger environment
	// overrides also verify early rejection before expanded lowering.
	compareCount := ir.MaxPredicateNodes / 8
	if v := os.Getenv("NYANJIT_PREDICATE_COMPARE_COUNT"); v != "" {
		var err error
		compareCount, err = strconv.Atoi(v)
		if err != nil || compareCount < 129 {
			t.Fatalf("invalid comparison count %q", v)
		}
	}
	for _, shape := range []string{"leftAnd", "rightAnd", "leftOr", "rightOr", "rangeOr", "balanced", "mixedNot"} {
		t.Run(shape, func(t *testing.T) {
			b := builder.NewBuilder()
			x, _ := b.Param()
			cmp := func(condition ir.BranchCondition, k int) builder.Predicate {
				// Exercise folding Compare(Const) as well as comparisons with immediates.
				var p builder.Predicate
				var err error
				if k%2 == 0 {
					v, _ := b.Const(uint64(k))
					p, err = b.Compare(x, condition, v)
				} else {
					p, err = b.CompareImmediate(x, condition, int64(k))
				}
				if err != nil {
					t.Fatal(err)
				}
				return p
			}
			count := compareCount
			if shape == "balanced" || shape == "rangeOr" {
				count = ir.MaxPredicateNodes / 8
			}
			condition := ir.BranchEqual
			if shape == "leftAnd" || shape == "rightAnd" {
				condition = ir.BranchNotEqual
			}
			p := cmp(condition, 0)
			if shape == "balanced" {
				level := make([]builder.Predicate, count)
				for i := range level {
					level[i] = cmp(condition, i)
				}
				for len(level) > 1 {
					next := make([]builder.Predicate, 0, (len(level)+1)/2)
					for i := 0; i < len(level); i += 2 {
						if i+1 == len(level) {
							next = append(next, level[i])
						} else {
							next = append(next, level[i].Or(level[i+1]))
						}
					}
					level = next
				}
				p = level[0]
			} else {
				for i := 1; i < count; i++ {
					q := cmp(condition, i)
					switch shape {
					case "leftAnd":
						p = p.And(q)
					case "rightAnd":
						p = q.And(p)
					case "leftOr":
						p = p.Or(q)
					case "rightOr":
						p = q.Or(p)
					case "rangeOr":
						p = p.Or(cmp(ir.BranchGreaterEqual, i*3).And(cmp(ir.BranchLessEqual, i*3+1)))
					case "mixedNot":
						p = p.Not().And(q.Not()).Not()
					}
				}
			}
			yes, no := b.NewLabel(), b.NewLabel()
			// One-target Branch also checks validation before adding a fallthrough block.
			expanded := 2*count - 1
			if shape == "rangeOr" {
				expanded = 4*count - 3
			}
			if shape == "mixedNot" {
				expanded = 5*count - 4
			}
			if err := b.Branch(p, yes); err != nil {
				if expanded > ir.MaxPredicateNodes && errors.Is(err, builder.ErrBackendLimit) {
					return
				}
				t.Fatal(err)
			} else if expanded > ir.MaxPredicateNodes {
				t.Fatal("oversized chain accepted")
			}
			if err := b.Branch(builder.Predicate{}, no); err != nil {
				t.Fatal(err)
			}
			if err := b.Bind(yes); err != nil {
				t.Fatal(err)
			}
			one, _ := b.Const(1)
			if err := b.Return(one); err != nil {
				t.Fatal(err)
			}
			if err := b.Bind(no); err != nil {
				t.Fatal(err)
			}
			zero, _ := b.Const(0)
			if err := b.Return(zero); err != nil {
				t.Fatal(err)
			}
			program, err := testFinishRaw(b)
			if err != nil {
				t.Fatal(err)
			}
			code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			for _, x := range []int{0, 1, count - 1, count, count*3 - 2, count * 3} {
				want := x < count
				switch shape {
				case "leftAnd", "rightAnd":
					want = !want
				case "rangeOr":
					want = x == 0 || x >= 3 && x <= (count-1)*3+1 && x%3 <= 1
				case "mixedNot":
					want = x == 0
					for i := 1; i < count; i++ {
						want = want || x == i
					}
				}
				args := []uintptr{uintptr(x)}
				if runtime.GOARCH == "386" {
					args = append(args, 0)
				} // Param is a uint64 pair.
				got, err := code.Call(args...)
				expected := uintptr(0)
				if want {
					expected = 1
				}
				if err != nil || got != expected {
					t.Fatalf("x%d: %d,%v want%d", x, got, err, expected)
				}
			}
		})
	}
}
