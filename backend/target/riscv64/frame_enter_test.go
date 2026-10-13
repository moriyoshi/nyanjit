package riscv64

import (
	"encoding/binary"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// Wherever the prologue has opened the frame, RA is already at 0(SP) and the
// Go frame's SP table gives SP's depth (security audit 2026-10-25, L24).
func TestFrameEnterStoresRABeforeMovingSP(t *testing.T) {
	for _, frame := range []int{16, 2032, 2048, 2064, 4096, 6096, 6112, 10000, 1 << 20} {
		a := NewByteAssembler(abi.StackPlatform, 0)
		a.FrameEnter(frame)
		code := a.Bytes()
		if len(code) != FrameEnterSize(frame) {
			t.Fatalf("frame %d: %d bytes, FrameEnterSize %d", frame, len(code), FrameEnterSize(frame))
		}
		goFrame, err := GoFrame([]machine.LocatedInstruction{{Instruction: machine.Instruction{Op: machine.OpFrameEnter, Imm: uint64(frame)}}}, len(code), frame)
		if err != nil {
			t.Fatalf("frame %d: GoFrame: %v", frame, err)
		}
		const entry = int64(1 << 30)
		regs := map[uint32]int64{2: entry}
		memory := map[int64]uint32{}
		for pc := 0; pc < len(code); pc += 4 {
			word := binary.LittleEndian.Uint32(code[pc:])
			rd, rs1, rs2 := word>>7&31, word>>15&31, word>>20&31
			switch {
			case word&0x707f == 0x13: // ADDI
				if rd != 0 {
					regs[rd] = regs[rs1] + int64(int32(word)>>20)
				}
			case word&0x707f == 0x3023: // SD
				memory[regs[rs1]+int64(int32(word&0xfe000000)>>20|int32(rd))] = rs2
			default:
				t.Fatalf("frame %d: unexpected word %#08x", frame, word)
			}
			sp := regs[2]
			if sp != entry && memory[sp] != 1 {
				t.Fatalf("frame %d: SP moved by %d at PC %d before RA was stored at 0(SP)", frame, entry-sp, pc+4)
			}
			delta := 0
			for _, point := range goFrame.SP {
				if point.PC <= pc+4 {
					delta = point.Delta
				}
			}
			if int64(delta) != entry-sp {
				t.Fatalf("frame %d: SP table gives %d at PC %d, SP moved by %d", frame, delta, pc+4, entry-sp)
			}
		}
		if regs[2] != entry-int64(frame) {
			t.Fatalf("frame %d: SP moved by %d", frame, entry-regs[2])
		}
	}
}
