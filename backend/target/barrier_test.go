package target_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/amd64"
	"github.com/moriyoshi/nyanjit/backend/target/arm64"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
	"github.com/moriyoshi/nyanjit/backend/target/riscv64"
)

// Encodings were checked against Go's disassembler (cmd/objdump).
func TestBarrierContractsAndEncodings(t *testing.T) {
	const (
		none   = machine.BarrierUnavailable
		isa    = machine.BarrierArchitectural
		config = machine.BarrierPlatform
	)
	type want struct {
		contract machine.BarrierContract
		code     []byte
	}
	x86 := map[machine.Barrier]want{
		machine.BarrierSpeculation: {config, []byte{0x0f, 0xae, 0xe8}}, // LFENCE
		machine.BarrierMemory:      {isa, []byte{0x0f, 0xae, 0xf0}},    // MFENCE
		machine.BarrierInstruction: {none, nil},
	}
	cases := map[string]struct {
		policy interface {
			Barrier(machine.Barrier) machine.BarrierContract
		}
		want map[machine.Barrier]want
	}{
		"amd64": {amd64.Policy{}, x86},
		"i386":  {i386.Policy{}, x86},
		"arm64": {arm64.Policy{}, map[machine.Barrier]want{
			machine.BarrierSpeculation: {isa, []byte{0x9f, 0x3f, 0x03, 0xd5, 0xdf, 0x3f, 0x03, 0xd5}}, // DSB SY; ISB
			machine.BarrierMemory:      {isa, []byte{0xbf, 0x3b, 0x03, 0xd5}},                         // DMB ISH
			machine.BarrierInstruction: {isa, []byte{0xdf, 0x3f, 0x03, 0xd5}},                         // ISB
		}},
		"ppc64": {ppc64.Policy{}, map[machine.Barrier]want{
			machine.BarrierSpeculation: {config, []byte{0x00, 0x00, 0xff, 0x63}}, // ori r31,r31,0
			machine.BarrierMemory:      {isa, []byte{0xac, 0x04, 0x00, 0x7c}},    // hwsync
			machine.BarrierInstruction: {isa, []byte{0x2c, 0x01, 0x00, 0x4c}},    // isync
		}},
		"riscv64": {riscv64.Policy{}, map[machine.Barrier]want{
			machine.BarrierSpeculation: {none, nil},
			machine.BarrierMemory:      {isa, []byte{0x0f, 0x00, 0x30, 0x03}}, // fence rw, rw
			machine.BarrierInstruction: {isa, []byte{0x0f, 0x10, 0x00, 0x00}}, // fence.i
		}},
		"loong64": {loong64.Policy{}, map[machine.Barrier]want{
			machine.BarrierSpeculation: {none, nil},
			machine.BarrierMemory:      {isa, []byte{0x00, 0x00, 0x72, 0x38}}, // dbar 0
			machine.BarrierInstruction: {isa, []byte{0x00, 0x80, 0x72, 0x38}}, // ibar 0
		}},
	}
	for _, target := range formTargets(t) {
		c, ok := cases[target.dir]
		if !ok {
			t.Fatalf("no barrier expectations for %s", target.dir)
		}
		for kind := machine.Barrier(0); kind <= machine.BarrierInstruction+1; kind++ {
			w := c.want[kind]
			if got := c.policy.Barrier(kind); got != w.contract {
				t.Errorf("%s barrier %d: contract %d, want %d", target.dir, kind, got, w.contract)
			}
			ins := machine.Instruction{Op: machine.OpBarrier, Barrier: kind}
			code, err := target.assemble(ins)
			switch {
			case !kind.Valid():
				if !errors.Is(err, machine.ErrInvalidInstruction) {
					t.Errorf("%s barrier %d: %v, want ErrInvalidInstruction", target.dir, kind, err)
				}
			case w.contract == none:
				if !errors.Is(err, machine.ErrUnsupportedOperation) {
					t.Errorf("%s barrier %d: %v, want ErrUnsupportedOperation", target.dir, kind, err)
				}
			case err != nil || !bytes.Equal(code, w.code):
				t.Errorf("%s barrier %d: % x, %v; want % x", target.dir, kind, code, err, w.code)
			}
			if size := target.size(ins); size != len(w.code) {
				t.Errorf("%s barrier %d: size %d, want %d", target.dir, kind, size, len(w.code))
			}
		}
	}
}
