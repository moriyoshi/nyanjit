//go:build linux && (amd64 || arm64 || 386 || riscv64 || loong64 || ppc64le)

package execution

import (
	"errors"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"reflect"
	"testing"
)

type functionExporter interface{ Func(any) error }

func TestCodeFuncDestinationValidation(t *testing.T) {
	c, err := NewCode(securityReturnBytes(), CodeOptions{ABI: abi.Go}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	exporter, ok := any(c).(functionExporter)
	if !ok {
		t.Fatal("Code.Func(fptr any) error is missing")
	}
	var nilFuncPtr *func()
	for _, input := range []any{nil, nilFuncPtr, func() {}, 123, new(uint64), new(any), new(*func())} {
		if err := exporter.Func(input); !errors.Is(err, ErrSignature) {
			t.Fatalf("destination %T: %v", input, err)
		}
	}
	original := func(x uint64) uint64 { return x + 77 }
	f := original
	if err := exporter.Func(&f); !errors.Is(err, ErrSignature) {
		t.Fatal(err)
	}
	if reflect.ValueOf(f).Pointer() != reflect.ValueOf(original).Pointer() {
		t.Fatal("signature error changed destination")
	}
	if c.funcExposed {
		t.Fatal("invalid destinations quarantined mapping")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if c.mem != nil {
		t.Fatal("failed function exports retained mapping")
	}
}

func TestCodeFuncErrorsPreserveDestination(t *testing.T) {
	original := func() {}
	f := original
	ptr := reflect.ValueOf(f).Pointer()
	c, err := NewCode(securityReturnBytes(), CodeOptions{ABI: abi.C}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	exporter, ok := any(c).(functionExporter)
	if !ok {
		t.Fatal("Code.Func missing")
	}
	if err := exporter.Func(&f); !errors.Is(err, ErrInvalidCodeOptions) {
		t.Fatal(err)
	}
	if reflect.ValueOf(f).Pointer() != ptr {
		t.Fatal("ABI error changed destination")
	}
	c.Close()
	if err := exporter.Func(&f); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if reflect.ValueOf(f).Pointer() != ptr {
		t.Fatal("closed error changed destination")
	}
	var absent *Code
	nilExporter := any(absent).(functionExporter)
	if err := nilExporter.Func(&f); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if reflect.ValueOf(f).Pointer() != ptr {
		t.Fatal("nil receiver changed destination")
	}
}

func TestCodeFuncNamedFunctionDestination(t *testing.T) {
	c, err := NewCode(securityReturnBytes(), CodeOptions{ABI: abi.Go}, nil)
	if err != nil {
		t.Fatal(err)
	}
	exporter, ok := any(c).(functionExporter)
	if !ok {
		c.Close()
		t.Fatal("Code.Func missing")
	}
	type Callback func() uint64
	var f Callback
	if err := exporter.Func(&f); err != nil {
		c.Close()
		t.Fatal(err)
	}
	if f == nil {
		t.Fatal("function was not assigned")
	}
	c.Close()
	if c.mem == nil {
		t.Fatal("function mapping not retained")
	}
	f()
}
