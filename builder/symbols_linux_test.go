//go:build linux && (arm64 || amd64 || riscv64)

package builder

import (
	"errors"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestSymbolTemplateBindingsAreIndependent(t *testing.T) {
	b := NewBuilder()
	symbol, err := b.Symbol("answer")
	requireControlOK(t, err)
	requireControlOK(t, b.Return(symbol))
	f := compile.Func{ABI: abi.C}
	external := compile.Func{ABI: abi.C, Context: compile.ContextExternal}
	template, err := testTemplate(b, compile.TargetSpec{}, external)
	requireControlOK(t, err)
	if _, err := template.Bytes(nil); !errors.Is(err, ErrUnboundSymbol) {
		t.Fatalf("Bytes without bindings = %v, want %v", err, ErrUnboundSymbol)
	}
	if _, err := testCompile(b); !errors.Is(err, ErrUnboundSymbol) {
		t.Fatalf("Compile without bindings = %v, want %v", err, ErrUnboundSymbol)
	}
	if relocations := template.Relocations(); len(relocations) != 1 || relocations[0].Symbol != "answer" {
		t.Fatalf("unexpected template relocations: %+v", relocations)
	}
	first, err := template.Bytes(map[string]uint64{"answer": 41})
	requireControlOK(t, err)
	if template.Report().CodeBytes() != len(first) {
		t.Fatalf("linked %d bytes, report = %+v", len(first), template.Report().Snapshot())
	}
	second, err := template.Bytes(map[string]uint64{"answer": 42})
	requireControlOK(t, err)
	again, err := template.Bytes(map[string]uint64{"answer": 41})
	requireControlOK(t, err)
	if slices.Equal(first, second) || !slices.Equal(first, again) {
		t.Fatal("binding did not produce independent copies of template bytes")
	}
	prepared, err := testTemplate(b, compile.TargetSpec{}, f)
	requireControlOK(t, err)
	relocations := prepared.Relocations()
	if len(relocations) != 1 || relocations[0].Symbol != "answer" {
		t.Fatalf("Prepare relocations = %+v", relocations)
	}
	relocations[0].Symbol = "mutated"
	if prepared.Relocations()[0].Symbol != "answer" {
		t.Fatal("Relocations returned the template's mutable slice")
	}
	for _, want := range []uint64{41, 42} {
		code, err := prepared.Bind(map[string]uint64{"answer": want})
		requireControlOK(t, err)
		got, err := code.Call()
		requireControlOK(t, err)
		requireControlOK(t, code.Close())
		if uint64(got) != want {
			t.Fatalf("binding answer=%d returned %d", want, got)
		}
	}
	bound, err := testTemplate(b, compile.TargetSpec{}, f)
	requireControlOK(t, err)
	code, err := bound.Bind(map[string]uint64{"answer": 43})
	requireControlOK(t, err)
	defer code.Close()
	got, err := code.Call()
	requireControlOK(t, err)
	if got != 43 {
		t.Fatalf("Bind returned %d, want 43", got)
	}
}

func TestSymbolBindingRejectsMissingAndExtraNames(t *testing.T) {
	b := NewBuilder()
	symbol, err := b.Symbol("answer")
	requireControlOK(t, err)
	requireControlOK(t, b.Return(symbol))
	template, err := testTemplate(b, compile.TargetSpec{}, compile.Func{ABI: abi.C})
	requireControlOK(t, err)
	for _, tc := range []struct {
		name     string
		bindings map[string]uint64
		want     error
	}{
		{name: "missing", bindings: map[string]uint64{}, want: ErrUnboundSymbol},
		{name: "extra", bindings: map[string]uint64{"answer": 42, "other": 1}, want: ErrInvalidFunction},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := template.Bind(tc.bindings); !errors.Is(err, tc.want) {
				t.Fatalf("Bind(%v) = %v, want %v", tc.bindings, err, tc.want)
			}
		})
	}
}
