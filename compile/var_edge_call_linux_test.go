//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le || 386)

package compile_test

import (
	"encoding/binary"
	"errors"
	"os"
	"runtime"
	"strconv"
	"testing"
	"unsafe"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
)

func zzCaller(np int, target uintptr, base uintptr) *n.Builder {
	b := n.NewBuilder()
	p := b.ParamNative()
	count := b.ParamNative()
	loop, exit := b.NewLabel(), b.NewLabel()
	args := []n.Value{count}
	w := []uint8{n.NativeBits}
	for i := 0; i < np; i++ {
		args = append(args, b.LoadBits(b.Add(p, b.ConstNative(uintptr(i*int(n.NativeBits/8)))), n.NativeBits))
		w = append(w, n.NativeBits)
	}
	b.JumpArgs(loop, args...)
	s := b.BindParamsBits(loop, w)
	called := b.CastNative(b.CallC(b.ConstNative(target), b.ConstNative(base), b.ConstNative(1)))
	next := []n.Value{b.Sub(s[0], b.ConstNative(1))}
	for i := 1; i < len(s); i++ {
		next = append(next, b.Add(b.Mul(s[i], b.ConstNative(3)), b.Xor(s[(i%(len(s)-1))+1], called)))
	}
	b.BranchArgs(b.IsZero(s[0]), exit, s, loop, next)
	r := b.BindParamsBits(exit, w)
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
	cb := zzEarly(14)
	if os.Getenv("ZZSIMPLE") != "" {
		cb = n.NewBuilder()
		x, y := cb.ParamNative(), cb.ParamNative()
		cb.Return(cb.Add(x, y))
	}
	cp, _ := cb.Finish()
	callee, err := compile.Compile(cp, abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 8192})
	if err != nil {
		t.Fatal(err)
	}
	defer callee.Close()
	addr, err := callee.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	// EntryAddress is raw code. Go 1.26 big-endian PPC64 defaults to
	// ELFv1 C calls, which consume a descriptor rather than a code pointer.
	if convention, _ := abi.DefaultPPC64CABI(runtime.GOARCH); convention == abi.PPC64ELFv1 {
		descriptor, err := execution.Scratch(24)
		if err != nil {
			t.Fatal(err)
		}
		defer execution.FreeScratch(descriptor)
		binary.NativeEndian.PutUint64(descriptor, uint64(addr))
		addr = uintptr(unsafe.Pointer(&descriptor[0]))
	}
	called := zzEarlyRef(values, 14, 1)
	if os.Getenv("ZZSIMPLE") != "" {
		called = base + 1
	}
	if g, _ := callee.Call(base, 1); g != called {
		t.Errorf("callee direct: got %x want %x", g, called)
	}
	for _, np := range []int{4, 8, 12, 16, 20} {
		p, err := zzCaller(np, uintptr(addr), base).Finish()
		if err != nil {
			t.Fatal(err)
		}
		want := zzCallerRef(values, np, 3, called)
		if os.Getenv("ZZDUMP") == strconv.Itoa(np) {
			asm, err := compile.EmitAssembly(p, abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 8192})
			os.WriteFile(os.Getenv("ZZOUT"), []byte(asm), 0o644)
			t.Log(err)
		}
		for i, mk := range []func() (*compile.Code, error){
			func() (*compile.Code, error) {
				return compile.CompileForeign(p, abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 8192})
			},
			func() (*compile.Code, error) {
				return compile.CompileGo(p, abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 8192, AssumePointerFree: true})
			},
			func() (*compile.Code, error) {
				return compile.CompileGo(p, abi.CompileOptions{ABI: abi.Go, Stack: abi.StackPlatform, StackBytesAvailable: 8192, AssumePointerFree: true})
			},
		} {
			c, err := mk()
			if err != nil {
				if i == 0 || !errors.Is(err, compile.ErrBackendLimit) {
					t.Errorf("np%d mode%d: %v", np, i, err)
				} else {
					t.Logf("np%d managed mode%d exceeds the registered frame limit", np, i)
				}
				continue
			}
			got, err := c.Call(base, 3)
			c.Close()
			if err != nil || got != want {
				t.Errorf("np%d mode%d: got %x want %x err %v", np, i, got, want, err)
			}
		}
	}
}
