//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le || 386)

package compile_test

import (
	"errors"
	"testing"
	"unsafe"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func zzEarly(np int) *n.Builder {
	b := n.NewBuilder()
	base := b.ParamNative()
	count := b.ParamNative()
	loop, exit, bail := b.NewLabel(), b.NewLabel(), b.NewLabel()
	args := []n.Value{count}
	w := []uint8{n.NativeBits}
	for i := 0; i < np; i++ {
		args = append(args, b.LoadBits(b.Add(base, b.ConstNative(uintptr(i*int(n.NativeBits/8)))), n.NativeBits))
		w = append(w, n.NativeBits)
	}
	b.JumpArgs(loop, args...)
	s := b.BindParamsBits(loop, w)
	next := []n.Value{b.Sub(s[0], b.ConstNative(1))}
	for i := 1; i < len(s); i++ {
		next = append(next, b.Add(b.Mul(s[i], b.ConstNative(3)), s[(i%(len(s)-1))+1]))
	}
	b.BranchArgs(b.IsZero(s[0]), exit, s, bail, next)
	t := b.BindParamsBits(bail, w)
	early := b.NewLabel()
	b.BranchArgs(b.IsZero(b.Sub(t[0], b.ConstNative(2))), early, t, loop, t)
	e := b.BindParamsBits(early, w)
	et := e[0]
	for _, x := range e[1:] {
		et = b.Add(b.Mul(et, b.ConstNative(17)), x)
	}
	b.Return(et)
	r := b.BindParamsBits(exit, w)
	total := r[0]
	for _, x := range r[1:] {
		total = b.Xor(b.Mul(total, b.ConstNative(31)), x)
	}
	b.Return(total)
	return b
}

func zzEarlyRef(mem []uintptr, np int, count uintptr) uintptr {
	s := []uintptr{count}
	for i := 0; i < np; i++ {
		s = append(s, mem[i])
	}
	for {
		if s[0] == 0 {
			total := s[0]
			for _, x := range s[1:] {
				total = total*31 ^ x
			}
			return total
		}
		next := []uintptr{s[0] - 1}
		for i := 1; i < len(s); i++ {
			next = append(next, s[i]*3+s[(i%(len(s)-1))+1])
		}
		s = next
		if s[0] == 2 {
			et := s[0]
			for _, x := range s[1:] {
				et = et*17 + x
			}
			return et
		}
	}
}

func TestVarEdgeMultipleExits(t *testing.T) {
	mem, values := zzMemory()
	for _, np := range []int{4, 8, 14, 20, 28} {
		p, err := zzEarly(np).Finish()
		if err != nil {
			t.Fatal(err)
		}
		for _, count := range []uintptr{1, 5} {
			want := zzEarlyRef(values, np, count)
			for i, cfg := range []abi.CompileOptions{
				{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 8192},
				{ABI: abi.Go, Stack: abi.StackPlatform, StackBytesAvailable: 8192},
			} {
				c, err := compile.Compile(p, cfg)
				if err != nil {
					t.Errorf("np%d cfg%d compile %v", np, i, err)
					continue
				}
				got, err := c.Call(uintptr(unsafe.Pointer(&mem[0])), count)
				c.Close()
				if err != nil || got != want {
					t.Errorf("np%d cfg%d count%d: got %x want %x err %v", np, i, count, got, want, err)
				}
			}
			for _, a := range []abi.Flavor{abi.C, abi.Go} {
				c, err := compile.CompileGo(p, abi.CompileOptions{ABI: a, Stack: abi.StackPlatform, StackBytesAvailable: 8192, AssumePointerFree: true})
				if err != nil {
					if !errors.Is(err, compile.ErrBackendLimit) || np < 14 {
						t.Errorf("np%d CompileGo abi%v: %v", np, a, err)
					} else {
						t.Logf("np%d managed ABI %v exceeds the registered frame limit", np, a)
					}
					continue
				}
				got, err := c.Call(uintptr(unsafe.Pointer(&mem[0])), count)
				c.Close()
				if err != nil || got != want {
					t.Errorf("np%d CompileGo abi%v count%d: got %x want %x err %v", np, a, count, got, want, err)
				}
			}
		}
	}
}
