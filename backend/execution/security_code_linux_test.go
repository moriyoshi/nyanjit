//go:build linux && (amd64 || arm64 || 386 || riscv64 || loong64 || ppc64le)

package execution

import (
	"errors"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"reflect"
	"runtime"
	"testing"
)

func securityReturnBytes() []byte {
	switch runtime.GOARCH {
	case "amd64", "386":
		return []byte{0xc3}
	case "arm64":
		return []byte{0xc0, 0x03, 0x5f, 0xd6}
	case "riscv64":
		return []byte{0x67, 0x80, 0, 0}
	case "loong64":
		return []byte{0x20, 0, 0, 0x4c}
	case "ppc64le":
		return []byte{0x20, 0, 0x80, 0x4e}
	}
	panic("unsupported test architecture")
}

func TestSecurityManagedCallsAcceptWords(t *testing.T) {
	for _, managed := range []bool{false, true} {
		for _, aggregate := range []bool{false, true} {
			options := CodeOptions{ABI: abi.C, GoManaged: managed}
			var args []uintptr
			if aggregate {
				options.Arity = 1
				options.ParameterTypes = []reflect.Type{reflect.TypeFor[*struct{ Value uint64 }]()}
				// The raw boundary cannot know these words' logical Go type. This
				// return-only fixture safely proves it does not perform that check.
				args = Words(new(uint64))
			}
			var register FrameRegistrar
			if managed {
				register = func([]byte) (bool, error) { return true, nil }
			}
			c, err := NewCode(securityReturnBytes(), options, register)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.Call(args...); err != nil {
				t.Errorf("managed=%v aggregate=%v Call: %v", managed, aggregate, err)
			}
			if _, _, err = c.CallPair(args...); err != nil {
				t.Errorf("managed=%v aggregate=%v CallPair: %v", managed, aggregate, err)
			}
			c.Close()
		}
	}
}

func TestSecurityFuncSurvivesClose(t *testing.T) {
	c, err := NewCode(securityReturnBytes(), CodeOptions{ABI: abi.Go}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 386 Go-ABI entries need an 8-byte result slot.
	var f func() uint64
	err = c.Func(&f)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	// Check before entry so the pre-fix regression fails without jumping into unmapped memory.
	if len(c.mem) == 0 {
		t.Fatal("Close unmapped a live Func")
	}
	runtime.GC()
	f()
	if _, err = c.Call(Words()...); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestSecurityNewCodeArity(t *testing.T) {
	_, err := NewCode(securityReturnBytes(), CodeOptions{Arity: abi.MaxArgumentsFor(runtime.GOARCH) + 1}, nil)
	if !errors.Is(err, ErrInvalidCodeOptions) {
		t.Fatalf("unpassable arity accepted: %v", err)
	}
}

func TestSecurityForeignZeroAddress(t *testing.T) {
	defer func() {
		if err, ok := recover().(error); !ok || !errors.Is(err, ErrInvalidCodeOptions) {
			t.Fatalf("zero address: %v", err)
		}
	}()
	CallForeignPair(0, nil)
}
