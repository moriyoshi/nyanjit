package compile

import (
	"reflect"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

func TestCachedCallAppendIsolation(t *testing.T) {
	spec := abi.CallSpec{Architecture: "arm64", Kind: abi.FunctionCall, ArgumentCount: 2}
	first, err := cachedCallEffect(spec)
	if err != nil {
		t.Fatal(err)
	}
	want := first.Clone()
	for _, v := range [][]string{first.ArgumentRegisters, first.ClobberedRegisters, first.ClobberedFPRegisters, first.ClobberedSpecialRegisters} {
		if cap(v) != len(v) {
			t.Fatal("unclipped cache slice")
		}
		v = append(v, "changed")
		v[len(v)-1] = "overwritten"
	}
	for _, loc := range first.ArgumentLocations {
		if cap(loc.Words) != len(loc.Words) {
			t.Fatal("unclipped words")
		}
		_ = append(loc.Words, abi.WordLocation{})
	}
	_ = append(first.ArgumentLocations, abi.ValueLocation{})
	_ = append(first.ResultLocation.Words, abi.WordLocation{})
	detached := first.Clone()
	if len(detached.ClobberedRegisters) > 0 {
		detached.ClobberedRegisters[0] = "changed"
	}
	got, err := cachedCallEffect(spec)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("cache changed: %v", err)
	}
}
func TestCachedEntryAppendIsolation(t *testing.T) {
	target := newMachineTarget(mustTarget(t, TargetSpec{Architecture: "arm64"}).options(Func{ABI: abi.C}))
	a, err := cachedEntryLayout(target, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	want := a.arguments[0].Words[0]
	if cap(a.arguments) != len(a.arguments) || cap(a.arguments[0].Words) != len(a.arguments[0].Words) || cap(a.result.Words) != len(a.result.Words) {
		t.Fatal("unclipped entry slices")
	}
	_ = append(a.arguments[0].Words, abi.WordLocation{})
	_ = append(a.arguments, abi.ValueLocation{})
	_ = append(a.result.Words, abi.WordLocation{})
	b, err := cachedEntryLayout(target, []int{1})
	if err != nil || b.arguments[0].Words[0] != want {
		t.Fatal("entry cache changed")
	}
}
func TestCachedRegisterAppendIsolation(t *testing.T) {
	target := newMachineTarget(mustTarget(t, TargetSpec{Architecture: "arm64"}).options(Func{ABI: abi.C}))
	for _, get := range []func() []physReg{func() []physReg { return cachedAllocationRegisters(target, nil) }, func() []physReg { return cachedPreservedRegisters(target) }} {
		a := get()
		want := slices.Clone(a)
		if cap(a) != len(a) {
			t.Fatal("unclipped registers")
		}
		_ = append(a, 255)
		if !reflect.DeepEqual(get(), want) {
			t.Fatal("register cache changed")
		}
	}
}
