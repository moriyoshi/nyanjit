//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || 386)

package compile_test

import (
	"errors"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// zzEarly is raw IR: many block parameters cross a loop with two exits.
func zzEarly(np int) *ir.SealedProgram {
	r := newRawIR()
	pb := r.pb
	entry := pb.Entry()
	bits := uint8(builder.NativeBits)
	native := ir.Scalar(bits)
	base := pb.Param(entry, native)
	count := pb.Param(entry, native)
	types := rawTypes(native, np+1)
	loop, exit, bail, early := pb.NewBlock(types...), pb.NewBlock(types...), pb.NewBlock(types...), pb.NewBlock(types...)
	args := []int{count}
	for i := 0; i < np; i++ {
		offset := r.constant(entry, bits, uint64(i*int(bits/8)))
		args = append(args, r.load(entry, bits, r.op(entry, ir.OpAdd, bits, base, offset)))
	}
	pb.Jump(entry, loop, args...)
	s := r.params(loop, np+1)
	next := []int{r.op(loop, ir.OpSub, bits, s[0], r.constant(loop, bits, 1))}
	for i := 1; i < len(s); i++ {
		next = append(next, r.op(loop, ir.OpAdd, bits, r.op(loop, ir.OpMul, bits, s[i], r.constant(loop, bits, 3)), s[(i%(len(s)-1))+1]))
	}
	pb.Branch(loop, rawIsZero(s[0]), exit, s, bail, next)
	t := r.params(bail, np+1)
	pb.Branch(bail, rawIsZero(r.op(bail, ir.OpSub, bits, t[0], r.constant(bail, bits, 2))), early, t, loop, t)
	e := r.params(early, np+1)
	et := e[0]
	for _, x := range e[1:] {
		et = r.op(early, ir.OpAdd, bits, r.op(early, ir.OpMul, bits, et, r.constant(early, bits, 17)), x)
	}
	pb.Return(early, et)
	results := r.params(exit, np+1)
	total := results[0]
	for _, x := range results[1:] {
		total = r.op(exit, ir.OpXor, bits, r.op(exit, ir.OpMul, bits, total, r.constant(exit, bits, 31)), x)
	}
	pb.Return(exit, total)
	return r.seal()
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
		p := zzEarly(np)
		for _, count := range []uintptr{1, 5} {
			want := zzEarlyRef(values, np, count)
			for i, cfg := range []compile.Func{
				{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}},
				{ABI: abi.Go, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}},
			} {
				c, err := n.Compile(p, compile.TargetSpec{}, cfg)
				if errors.Is(err, compile.ErrBackendLimit) {
					// Oversized goroutine frames must fail; exercise the same
					// control transfers using workspace storage instead.
					cfg.Stack.Practice = abi.StackNoUse
					c, err = n.Compile(p, compile.TargetSpec{}, cfg)
				}
				if err != nil {
					t.Errorf("np%d cfg%d compile %v", np, i, err)
					continue
				}
				got, err := c.Call(execution.Words(&mem[0], count)...)
				c.Close()
				if err != nil || got != want {
					t.Errorf("np%d cfg%d count%d: got %x want %x err %v", np, i, count, got, want, err)
				}
			}
			for _, a := range []abi.Flavor{abi.C, abi.Go} {
				c, err := n.Compile(p, compile.TargetSpec{}, compile.Func{ABI: a, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}, AssumePointerFree: true})
				if managedUnsupported(err) {
					continue
				}
				if err != nil {
					if !errors.Is(err, compile.ErrBackendLimit) || np < 14 {
						t.Errorf("np%d CompileGo abi%v: %v", np, a, err)
					} else {
						t.Logf("np%d managed ABI %v exceeds the registered frame limit", np, a)
					}
					continue
				}
				got, err := c.Call(execution.Words(&mem[0], count)...)
				c.Close()
				if err != nil || got != want {
					t.Errorf("np%d CompileGo abi%v count%d: got %x want %x err %v", np, a, count, got, want, err)
				}
			}
		}
	}
}
