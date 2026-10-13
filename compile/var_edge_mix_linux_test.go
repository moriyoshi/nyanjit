//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || 386)

package compile_test

import (
	"errors"
	"fmt"

	"testing"
	"unsafe"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
)

func zzMix(nv, np int) *n.Builder {
	b := n.NewBuilder()
	base := b.ParamNative()
	count := b.ParamNative()
	b.Enter()
	vars := make([]n.Var, nv)
	for i := range vars {
		vars[i] = b.Var(b.Add(b.LoadBits(b.Add(base, b.ConstNative(uintptr(i*int(builder.NativeBits/8)))), builder.NativeBits), b.ConstNative(uintptr(i))))
	}
	loop, exit := b.NewLabel(), b.NewLabel()
	// The loop state lives in Vars beside the live homes of vars.
	state := []n.Var{b.Var(count)}
	for i := 0; i < np; i++ {
		state = append(state, b.Var(b.LoadBits(b.Add(base, b.ConstNative(uintptr((32+i)*int(builder.NativeBits/8)))), builder.NativeBits)))
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
	next := []n.Value{b.Sub(s[0], b.ConstNative(1))}
	for i := 1; i < len(s); i++ {
		next = append(next, b.Add(b.Mul(s[i], b.ConstNative(3)), b.Get(vars[i%nv])))
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
	for _, v := range vars {
		total = b.Xor(b.Mul(total, b.ConstNative(31)), b.Get(v))
	}
	b.Leave(total)
	b.Return(total)
	return b
}

func zzMixRef(mem []uintptr, nv, np int, count uintptr) uintptr {
	vars := make([]uintptr, nv)
	for i := range vars {
		vars[i] = mem[i] + uintptr(i)
	}
	s := []uintptr{count}
	for i := 0; i < np; i++ {
		s = append(s, mem[32+i])
	}
	for s[0] != 0 {
		next := []uintptr{s[0] - 1}
		for i := 1; i < len(s); i++ {
			next = append(next, s[i]*3+vars[i%nv])
		}
		s = next
	}
	total := s[0]
	for _, x := range s[1:] {
		total = total*31 ^ x
	}
	for _, v := range vars {
		total = total*31 ^ v
	}
	return total
}

func TestVarEdgeWithLiveHomes(t *testing.T) {
	mem, values := zzMemory()

	for _, nv := range []int{4, 10, 16} {
		for _, np := range []int{4, 8, 12, 16} {
			p, err := zzMix(nv, np).Finish()
			if err != nil {
				t.Fatal(err)
			}
			want := zzMixRef(values, nv, np, 5)
			for _, cfg := range []compile.Func{
				{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}},
				{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackNoUse}},
				{ABI: abi.Go, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}},
			} {
				name := fmt.Sprintf("nv%d np%d abi%v stack%v", nv, np, cfg.ABI, cfg.Stack.Practice)
				c, err := n.Compile(p, compile.TargetSpec{}, cfg)
				if errors.Is(err, compile.ErrBackendLimit) {
					cfg.Stack.Practice = abi.StackNoUse
					c, err = n.Compile(p, compile.TargetSpec{}, cfg)
				}
				if err != nil {
					t.Errorf("%s: compile %v", name, err)
					continue
				}
				got, err := c.Call(uintptr(unsafe.Pointer(&mem[0])), 5)
				c.Close()
				if err != nil || got != want {
					t.Errorf("%s: got %x want %x err %v", name, got, want, err)
				}
			}
		}
	}
}
