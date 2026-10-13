package ppc64

import (
	"encoding/binary"
	"errors"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

func TestCPUFromAuxv(t *testing.T) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian} {
		for _, test := range []struct {
			caps uint64
			want abi.PPC64CPU
		}{{0, abi.PPC64Power8}, {0x80000000, abi.PPC64Power8}, {0x00800000, abi.PPC64Power9}, {0x00040000, abi.PPC64Power10}, {0x80840000, abi.PPC64Power10}} {
			auxv := make([]byte, 48)
			order.PutUint64(auxv, 3) // unrelated AT_PHDR
			order.PutUint64(auxv[8:], 0x12340000)
			order.PutUint64(auxv[16:], 26)
			order.PutUint64(auxv[24:], test.caps)
			if got := cpuFromAuxv(auxv, abi.PPC64Power8); got != test.want {
				t.Fatalf("%s caps=%#x: %s; want %s", order, test.caps, got, test.want)
			}
		}
		for _, auxv := range [][]byte{nil, make([]byte, 15), make([]byte, 31)} {
			if got := cpuFromAuxv(auxv, abi.PPC64Power9); got != abi.PPC64Power9 {
				t.Fatalf("truncated/absent auxv changed fallback: %s", got)
			}
		}
	}
}

func TestMultiplyAddCPUSelection(t *testing.T) {
	profile, err := abi.NewPPC64Profile("ppc64le", abi.C)
	if err != nil {
		t.Fatal(err)
	}
	ins := machine.Instruction{Op: machine.OpMulAdd, A: 3, B: 6, C: 4, D: 5}
	for _, level := range []abi.PPC64CPU{0, 8, 9, 10} {
		policy, err := NewPolicyForCPU(profile, abi.StackNoUse, level)
		if err != nil {
			t.Fatal(err)
		}
		emitter, err := NewEmitterForCPU(profile, abi.StackNoUse, false, level)
		if err != nil {
			t.Fatal(err)
		}
		sequence, err := emitter.Expand(ins, 20)
		if err != nil {
			t.Fatal(err)
		}
		fused := level >= abi.PPC64Power9
		if policy.CanMulAdd() != fused || (len(sequence) == 1) != fused {
			t.Fatalf("CPU %s: policy=%t sequence=%+v", level, policy.CanMulAdd(), sequence)
		}
		if fused {
			if got, err := Encode(sequence[0]); err != nil || got != 0x106429b3 {
				// Independently checked with Go tool asm:
				// MADDLD R4, R5, R6, R3 -> 10 64 29 b3.
				t.Fatalf("MADDLD=%#x,%v", got, err)
			}
			if !slices.Equal(emitter.ImplicitWrites(ins), []backend.Register{3}) {
				t.Fatalf("MADDLD temporaries: %v", emitter.ImplicitWrites(ins))
			}
		} else if sequence[0].Op != Mulld || sequence[1].Op != Add {
			t.Fatalf("POWER8 fallback=%+v", sequence)
		}
		if len(emitter.SpecialWrites(ins)) != 0 || emitter.FlagEffect(ins) {
			t.Fatal("integer multiply-add must preserve condition/status resources")
		}
		if emitter.Size(ins) != len(sequence)*4 {
			t.Fatalf("size differs from CPU expansion: %s", level)
		}
	}
	if _, err := NewPolicyForCPU(profile, abi.StackNoUse, 7); !errors.Is(err, abi.ErrInvalidSpec) {
		t.Fatalf("invalid CPU policy: %v", err)
	}
	if _, err := NewEmitterForCPU(profile, abi.StackNoUse, false, 7); !errors.Is(err, abi.ErrInvalidSpec) {
		t.Fatalf("invalid CPU emitter: %v", err)
	}
}
