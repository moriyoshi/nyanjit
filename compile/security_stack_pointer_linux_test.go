//go:build linux && (amd64 || arm64 || 386 || riscv64 || loong64 || ppc64le)

package compile_test

import (
	"fmt"
	"reflect"
	"testing"
	"unsafe"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
)

//go:noinline
func securityGrowStack(depth int) uint64 {
	var block [4096]byte
	block[depth] = 1
	if depth == 0 {
		return uint64(block[0])
	}
	return securityGrowStack(depth-1) + uint64(block[depth])
}

//go:noinline
func securityGrowingCallee(_ uintptr) uint64 { return securityGrowStack(64) }

//go:noinline
func securityStackPointerCall(code interface {
	Call(...uintptr) (uintptr, error)
}) error {
	var local uint64
	before := uintptr(unsafe.Pointer(&local))
	got, e := code.Call(execution.Words(&local)...)
	after := uintptr(unsafe.Pointer(&local))
	if e != nil {
		return e
	}
	if got != after || before != after {
		return fmt.Errorf("stack address moved %#x -> %#x, returned %#x", before, after, got)
	}
	return nil
}
func TestSecurityCallPointerEscapes(t *testing.T) {
	b := n.NewBuilder()
	p := b.ParamNative()
	b.CallGo(b.ConstNative(reflect.ValueOf(securityGrowingCallee).Pointer()), p)
	// Compare addresses after stack growth without dereferencing the old address.
	// If Words loses heap escape/root ownership, this fails safely.
	b.Return(p)
	s, e := b.Finish()
	if e != nil {
		t.Fatal(e)
	}
	c, e := n.Compile(s, compile.TargetSpec{}, compile.Func{ABI: abi.C, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}, AssumePointerFree: true})
	if managedUnsupported(e) {
		t.Skip("Go-managed code needs goregister:", e)
	}
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	done := make(chan error, 1)
	go func() { done <- securityStackPointerCall(c) }()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
