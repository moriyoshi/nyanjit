//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"errors"
	"reflect"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/compile"
)

func ordinaryGoTarget(x uint64) uint64 { return x + 1 }

func TestGeneratedGoCallNestingRejected(t *testing.T) {
	for _, kind := range []string{"leaf", "registered", "foreign"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "registered" && !goregister.Supported() {
				t.Skip("Go-managed code needs goregister")
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				b := n.NewBuilder()
				x := b.Param()
				b.Return(x)
				p, err := b.Finish()
				if err != nil {
					t.Error(err)
					return
				}
				managed := compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}, AssumePointerFree: true}
				leafFunc := managed
				leafFunc.Context = compile.ContextNative
				if kind == "registered" {
					leafFunc = managed
				} else if kind == "foreign" {
					leafFunc.ABI = abi.C
					leafFunc.Context = compile.ContextForeign
				}
				leaf, err := n.Compile(p, compile.TargetSpec{}, leafFunc)
				if err != nil {
					t.Error(err)
					return
				}
				defer leaf.Close()
				entry, err := leaf.EntryAddress()
				if err != nil {
					t.Error(err)
					return
				}
				b = n.NewBuilder()
				x = b.Param()
				b.Return(b.CallGo(b.ConstNative(entry), x))
				p, err = b.Finish()
				if err != nil {
					t.Error(err)
					return
				}
				caller, err := n.Compile(p, compile.TargetSpec{}, managed)
				if caller != nil {
					caller.Close()
				}
				if !errors.Is(err, compile.ErrInvalidFunction) {
					t.Errorf("generated nested target accepted: %v", err)
				}
			}()
			<-done
		})
	}
}

func TestGeneratedGoCallRecursionRejected(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		b := n.NewBuilder()
		fn := b.Var(b.ParamNative())
		depth := b.Var(b.Param())
		base, rec := b.NewLabel(), b.NewLabel()
		b.Branch(b.IsZero(b.Get(depth)), base)
		b.Jump(rec)
		b.Bind(rec)
		b.Return(b.CallGo(b.Get(fn), b.Get(fn), b.Sub(b.Get(depth), b.Const(1))))
		b.Bind(base)
		b.Return(b.Const(0))
		p, err := b.Finish()
		if err != nil {
			t.Error(err)
			return
		}
		_, err = lowerTemplate(p, compile.TargetSpec{}, compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}, AssumePointerFree: true})
		if !errors.Is(err, compile.ErrInvalidFunction) {
			t.Errorf("dynamic recursive target accepted: %v", err)
		}
	}()
	<-done
}

func TestFixedGoCallBindingsChecked(t *testing.T) {
	b := n.NewBuilder()
	x := b.Param()
	b.Return(b.CallGo(b.CastNative(b.Symbol("callback")), x))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	template, err := lowerTemplate(p, compile.TargetSpec{}, compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}, AssumePointerFree: true})
	if err != nil {
		t.Fatal(err)
	}
	address := uint64(reflect.ValueOf(ordinaryGoTarget).Pointer())
	for _, bad := range []uint64{1, address + 1} {
		if c, err := template.Bind(map[string]uint64{"callback": bad}); !errors.Is(err, compile.ErrInvalidFunction) {
			if c != nil {
				c.Close()
			}
			t.Fatalf("invalid entry %#x accepted: %v", bad, err)
		}
	}
	c, err := template.Bind(map[string]uint64{"callback": address})
	if managedUnsupported(err) {
		t.Skip("Go-managed code needs goregister:", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	checkedIndexResult(t, c, 42, uint64(41))
}
