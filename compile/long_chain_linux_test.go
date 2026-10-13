//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime/debug"
	"strconv"
	"testing"
	"time"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
)

// A small stack makes this a cheap regression for both formerly recursive
// selector walks. A subprocess contains the runtime's unrecoverable overflow.
// NYANJIT_LONG_CHAIN permits the review's full 1.2-million-op reproduction.
func TestLongValueChainCompilation(t *testing.T) {
	if os.Getenv("NYANJIT_LONG_CHAIN_CHILD") != "1" {
		for _, style := range []string{"instructions", "expressions"} {
			t.Run(style, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLongValueChainCompilation$", "-test.v", "-test.timeout=4m")
				cmd.Env = append(os.Environ(), "NYANJIT_LONG_CHAIN_CHILD=1", "NYANJIT_LONG_CHAIN_STYLE="+style)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("long-chain subprocess: %v\n%s", err, out)
				}
			})
		}
		return
	}
	count := 10000
	if value := os.Getenv("NYANJIT_LONG_CHAIN"); value != "" {
		var err error
		count, err = strconv.Atoi(value)
		if err != nil || count < 1 {
			t.Fatalf("invalid NYANJIT_LONG_CHAIN=%q", value)
		}
	}
	b := n.NewBuilder()
	x, y := b.Param(), b.Param()
	debug.SetMaxStack(1 << 20)
	value := x
	want := uint64(5)
	for i := 0; i < count; i++ {
		if i%2 == 0 {
			if os.Getenv("NYANJIT_LONG_CHAIN_STYLE") == "expressions" {
				value = value.Xor(y)
			} else {
				value = b.Xor(value, y)
			}
			want ^= 7
		} else {
			if os.Getenv("NYANJIT_LONG_CHAIN_STYLE") == "expressions" {
				value = value.Add(y)
			} else {
				value = b.Add(value, y)
			}
			want += 7
		}
	}
	if !b.Valid(value) {
		t.Fatal("long value chain is invalid")
	}
	b.Return(value)
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	c, err := n.Compile(p, compile.TargetSpec{}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 16}})
	if errors.Is(err, compile.ErrBackendLimit) || errors.Is(err, compile.ErrStackRequired) {
		t.Logf("bounded compile rejection: %v", err)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	lo, hi, err := c.CallPair(execution.Words(uint64(5), uint64(7))...)
	if got := execution.Wide(lo, hi); err != nil || got != want {
		t.Fatalf("got %#x, %v; want %#x", got, err, want)
	}
}
