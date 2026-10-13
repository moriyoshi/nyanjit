//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestArithmetic32SpillReloadIndex(t *testing.T) {
	b := builder.NewBuilder()
	must := func(v builder.Value, err error) builder.Value {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	x := must(b.ParamBits(32))
	values := make([]builder.Value, 96)
	for i := range values {
		values[i] = must(b.Add(x, must(b.ConstBits(32, uint64(i+1)))))
	}
	sum := must(b.Const(0))
	for _, v := range values[1:] {
		sum = must(b.Add(sum, must(b.CastUnsigned(v, 64))))
	}
	snapshot, err := b.Var(sum)
	if err != nil {
		t.Fatal(err)
	}
	index := must(b.CastUnsigned(values[0], 64))
	bad := b.NewLabel()
	address := must(b.IndexedAddress(must(b.Const(0x1000)), index, must(b.Const(1<<32-1)), 8, bad))
	result := must(b.Xor(address, must(b.Get(snapshot))))
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(bad); err != nil {
		t.Fatal(err)
	}
	if err := b.Return(must(b.Const(0xdead))); err != nil {
		t.Fatal(err)
	}
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	u, err := lowerUnit(p, TargetSpec{}, Func{ABI: abi.C, Context: ContextForeign, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
	if err != nil {
		t.Fatal(err)
	}
	// Track the first arithmetic result x+1 through physical copies, spills and
	// reloads. Other pressure values use x+2..x+96 and are not sufficient proof.
	tags := map[backend.Register]bool{}
	constants := map[backend.Register]uint64{}
	slots := map[int]bool{}
	spilled, reloaded := false, false
	spillCount, reloadCount := 0, 0
	for _, located := range u.physical.insns {
		i := located.ins
		switch i.Op {
		case machine.OpConst:
			constants[i.A] = i.Imm
			tags[i.A] = false
		case machine.OpMove:
			tags[i.A] = tags[i.B]
			constants[i.A] = constants[i.B]
		case machine.OpBinaryImmediate:
			tags[i.A] = i.Bits == 32 && i.Arithmetic == ir.OpAdd && i.Imm == 1
			delete(constants, i.A)
		case machine.OpInc:
			tags[i.A] = true
			delete(constants, i.A)
		case machine.OpBinary:
			tags[i.A] = i.Bits == 32 && i.Arithmetic == ir.OpAdd && constants[i.C] == 1
			delete(constants, i.A)
		case machine.OpNarrow:
			tags[i.A] = tags[i.B]
		case machine.OpSpill:
			slots[i.Slot] = tags[i.A]
			spilled = spilled || slots[i.Slot]
			if slots[i.Slot] {
				spillCount++
			}
		case machine.OpReload:
			tags[i.A] = slots[i.Slot]
			reloaded = reloaded || tags[i.A]
			if tags[i.A] {
				reloadCount++
			}
			delete(constants, i.A)
		}
	}
	if !spilled || !reloaded {
		listing, _ := u.Listing()
		t.Fatalf("x+1 did not actually spill/reload: spill=%v reload=%v\n%s", spilled, reloaded, listing)
	}
	template, err := u.Template()
	if err != nil {
		t.Fatal(err)
	}
	c, err := template.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	successes, failures := 0, 0
	for _, input := range []uint32{0, 0x7ffffffe, 0x7fffffff, 0x80000000, 0xfffffffd, 0xfffffffe, 0xffffffff} {
		index := uint64(input + 1)
		var sum uint64
		for i := uint32(2); i <= 96; i++ {
			sum += uint64(input + i)
		}
		want := uint64(0xdead)
		if index < 1<<32-1 {
			successes++
			want = (0x1000 + index*8) ^ sum
		} else {
			failures++
		}
		lo, hi, err := c.CallPair(execution.Words(input)...)
		if got := execution.Wide(lo, hi); err != nil || got != want {
			t.Fatalf("input %#x: got %#x %v want %#x", input, got, err, want)
		}
	}
	if successes == 0 || failures == 0 {
		t.Fatal("both bounds edges must execute")
	}
	t.Logf("%s: x+1 spills=%d reloads=%d; %d in-bounds, %d out-of-bounds inputs", u.report.Architecture, spillCount, reloadCount, successes, failures)
}
