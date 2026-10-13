//go:build linux && (arm64 || amd64 || riscv64)

package builder

import (
	"errors"
	"slices"
	"testing"
)

func TestSymbolTemplateBindingsAreIndependent(t *testing.T) {
	b := NewBuilder()
	symbol, err := b.Symbol("answer")
	requireControlOK(t, err)
	requireControlOK(t, b.Return(symbol))
	options := CompileOptions{ABI: ABIC}
	if _, _, err := testEmit(b, options); !errors.Is(err, ErrUnboundSymbol) {
		t.Fatalf("Emit without bindings = %v, want %v", err, ErrUnboundSymbol)
	}
	if _, err := testCompile(b); !errors.Is(err, ErrUnboundSymbol) {
		t.Fatalf("Compile without bindings = %v, want %v", err, ErrUnboundSymbol)
	}
	template, err := testEmitTemplate(b, options)
	requireControlOK(t, err)
	if len(template.Relocations) != 1 || template.Relocations[0].Symbol != "answer" ||
		template.Report.CodeBytes != len(template.Bytes) {
		t.Fatalf("unexpected template: %+v", template)
	}
	original := slices.Clone(template.Bytes)
	first, err := template.Link(map[string]uint64{"answer": 41})
	requireControlOK(t, err)
	second, err := template.Link(map[string]uint64{"answer": 42})
	requireControlOK(t, err)
	if slices.Equal(first, second) || !slices.Equal(template.Bytes, original) {
		t.Fatal("binding did not produce independent copies of template bytes")
	}
	prepared, err := testPrepare(b, options)
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
	code, err := testCompileWithBindings(b, options, map[string]uint64{"answer": 43})
	requireControlOK(t, err)
	defer code.Close()
	got, err := code.Call()
	requireControlOK(t, err)
	if got != 43 {
		t.Fatalf("CompileWithBindings returned %d, want 43", got)
	}
}

func TestSymbolBindingRejectsMissingAndExtraNames(t *testing.T) {
	b := NewBuilder()
	symbol, err := b.Symbol("answer")
	requireControlOK(t, err)
	requireControlOK(t, b.Return(symbol))
	template, err := testPrepare(b, CompileOptions{ABI: ABIC})
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
