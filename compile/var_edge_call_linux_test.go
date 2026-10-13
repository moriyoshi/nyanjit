//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || 386)

package compile_test

import (
	"errors"
	"os"
	"strconv"
	"testing"
	"unsafe"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
)

func zzCaller(np int, target uintptr, base uintptr) *n.Builder {
	b := n.NewBuilder()
	p := b.ParamNative()
	count := b.ParamNative()
	loop, exit := b.NewLabel(), b.NewLabel()
	// The loop state lives in Vars across the call.
	state := []n.Var{b.Var(count)}
	for i := 0; i < np; i++ {
		state = append(state, b.Var(b.LoadBits(b.Add(p, b.ConstNative(uintptr(i*int(builder.NativeBits/8)))), builder.NativeBits)))
	}
	get := func() []n.Value {
		values := make([]n.Value, len(state))
		for i, v := range state {
			values[i] = b.Get(v)
		}
		return values
	}
	b.Bind(loop)
	s := get()
	called := b.CastNative(b.CallC(b.ConstNative(target), b.ConstNative(base), b.ConstNative(1)))
	next := []n.Value{b.Sub(s[0], b.ConstNative(1))}
	for i := 1; i < len(s); i++ {
		next = append(next, b.Add(b.Mul(s[i], b.ConstNative(3)), b.Xor(s[(i%(len(s)-1))+1], called)))
	}
	b.Branch(b.IsZero(s[0]), exit)
	for i, v := range state {
		b.Set(v, next[i])
	}
	b.Jump(loop)
	b.Bind(exit)
	r := get()
	total := r[0]
	for _, x := range r[1:] {
		total = b.Xor(b.Mul(total, b.ConstNative(31)), x)
	}
	b.Return(total)
	return b
}

func zzCallerRef(mem []uintptr, np int, count, called uintptr) uintptr {
	s := []uintptr{count}
	for i := 0; i < np; i++ {
		s = append(s, mem[i])
	}
	for s[0] != 0 {
		next := []uintptr{s[0] - 1}
		for i := 1; i < len(s); i++ {
			next = append(next, s[i]*3+(s[(i%(len(s)-1))+1]^called))
		}
		s = next
	}
	total := s[0]
	for _, x := range s[1:] {
		total = total*31 ^ x
	}
	return total
}

func TestVarEdgeAcrossForeignCall(t *testing.T) {
	mem, values := zzMemory()
	base := uintptr(unsafe.Pointer(&mem[0]))
	cp := zzEarly(14).Clone()
	if os.Getenv("ZZSIMPLE") != "" {
		cb := n.NewBuilder()
		x, y := cb.ParamNative(), cb.ParamNative()
		cb.Return(cb.Add(x, y))
		cp, _ = testFinishRaw(cb)
	}
	callee, err := n.Compile(cp, compile.TargetSpec{}, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}})
	if err != nil {
		t.Fatal(err)
	}
	defer callee.Close()
	addr, err := callee.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	called := zzEarlyRef(values, 14, 1)
	if os.Getenv("ZZSIMPLE") != "" {
		called = base + 1
	}
	if g, _ := callee.Call(execution.Words(base, 1)...); g != called {
		t.Errorf("callee direct: got %x want %x", g, called)
	}
	for _, np := range []int{4, 8, 12, 16, 20} {
		p, err := zzCaller(np, uintptr(addr), base).Finish()
		if err != nil {
			t.Fatal(err)
		}
		want := zzCallerRef(values, np, 3, called)
		if os.Getenv("ZZDUMP") == strconv.Itoa(np) {
			asm, err := lowerListing(p, compile.TargetSpec{}, compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}})
			os.WriteFile(os.Getenv("ZZOUT"), []byte(asm), 0o644)
			t.Log(err)
		}
		for i, mk := range []func() (*execution.Code, error){
			func() (*execution.Code, error) {
				return n.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}})
			},
			func() (*execution.Code, error) {
				return n.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.C, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}, AssumePointerFree: true})
			},
			func() (*execution.Code, error) {
				return n.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}, AssumePointerFree: true})
			},
		} {
			c, err := mk()
			if i > 0 && managedUnsupported(err) {
				continue
			}
			if err != nil {
				if i == 0 || !errors.Is(err, compile.ErrBackendLimit) {
					t.Errorf("np%d mode%d: %v", np, i, err)
				} else {
					t.Logf("np%d managed mode%d exceeds the registered frame limit", np, i)
				}
				continue
			}
			got, err := c.Call(execution.Words(&mem[0], uintptr(3))...)
			c.Close()
			if err != nil || got != want {
				t.Errorf("np%d mode%d: got %x want %x err %v", np, i, got, want, err)
			}
		}
	}
}
