//go:build linux && (amd64 || arm64 || 386 || riscv64 || loong64 || ppc64le)

package compile_test

import (
	"errors"
	"testing"
	"unsafe"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func securityPressure(t *testing.T, count int) *ir.Program {
	t.Helper()
	b := n.NewBuilder()
	p := b.ParamNative()
	values := make([]n.Value, count)
	for i := range values {
		values[i] = b.Load64(b.Add(p, b.ConstNative(uintptr(i*8))))
	}
	sum := values[0]
	for _, v := range values[1:] {
		sum = b.Add(sum, v)
	}
	b.Return(sum)
	s, e := b.Finish()
	if e != nil {
		t.Fatal(e)
	}
	return s.Clone()
}

func TestSecurityGoroutineStackBudget(t *testing.T) {
	opts := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 20}}
	large, e := lowerTemplate(securityPressure(t, 300), compile.TargetSpec{}, opts)
	if e != nil {
		t.Fatal(e)
	}
	code, e := large.Bind(nil)
	if code != nil {
		code.Close()
	}
	if !errors.Is(e, compile.ErrBackendLimit) {
		t.Fatalf("%d-byte frame accepted: %v", large.Report().StackBytes(), e)
	}
	small, e := n.Compile(securityPressure(t, 4), compile.TargetSpec{}, opts)
	if e != nil {
		t.Fatal(e)
	}
	defer small.Close()
	done := make(chan error, 1)
	go func() {
		a := [4]uint64{1, 2, 3, 4}
		got, e := small.Call(uintptr(unsafe.Pointer(&a[0])))
		if e == nil && got != 10 {
			e = errors.New("wrong result on fresh goroutine")
		}
		done <- e
	}()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
