//go:build linux && (amd64 || arm64 || 386 || riscv64 || loong64 || ppc64le)

package compile_test

import (
	"errors"
	"testing"
	"time"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestSecurityCloseAndReenterFromCallback(t *testing.T) {
	var code *execution.Code
	depth := 0
	callback, err := execution.RegisterForeignCallback(func() uintptr {
		depth++
		if depth == 1 {
			value, err := code.Call(execution.Words()...)
			if err != nil || value != 41 {
				return 1
			}
			return value + 1
		}
		if err := code.Close(); err != nil {
			return 2
		}
		if _, err := code.Call(execution.Words()...); !errors.Is(err, execution.ErrClosed) {
			return 3
		}
		return 41
	})
	if err != nil {
		t.Fatal(err)
	}
	b := n.NewBuilder()
	b.Return(b.CallC(b.ConstNative(callback)))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err = n.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		value uintptr
		err   error
	}
	done := make(chan result, 1)
	go func() { v, e := code.Call(execution.Words()...); done <- result{v, e} }()
	select {
	case got := <-done:
		if got.err != nil || got.value != 42 {
			t.Fatalf("callback: %d, %v", got.value, got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close or reentrant Call deadlocked inside callback")
	}
	if err := code.Close(); err != nil {
		t.Fatal(err)
	}
}
