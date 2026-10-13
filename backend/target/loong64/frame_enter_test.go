package loong64

import (
	"encoding/binary"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// Wherever the prologue has opened the frame, r1 is already at 0(SP) and
// the Go frame's SP table gives SP's depth (security audit 2026-10-25, L24).
func TestFrameEnterStoresRABeforeMovingSP(t *testing.T) {
	for _, frame := range []int{16, 2032, 2048, 2064, 4096, 6096, 6112, 10000, 1 << 20} {
		a := NewByteAssembler(abi.StackPlatform, 0)
		a.FrameEnter(frame)
		code := a.Bytes()
		enter := machine.Instruction{Op: machine.OpFrameEnter, Imm: uint64(frame)}
		if len(code) != PhysicalSize(enter, abi.StackPlatform, 0) {
			t.Fatalf("frame %d: %d bytes, PhysicalSize %d", frame, len(code), PhysicalSize(enter, abi.StackPlatform, 0))
		}
		goFrame, err := GoFrame([]machine.LocatedInstruction{{Instruction: enter}}, len(code), frame)
		if err != nil {
			t.Fatalf("frame %d: GoFrame: %v", frame, err)
		}
		const entry = int64(1 << 30)
		regs := map[uint32]int64{3: entry}
		memory := map[int64]uint32{}
		for pc := 0; pc < len(code); pc += 4 {
			word := binary.LittleEndian.Uint32(code[pc:])
			d, j, imm := word&31, word>>5&31, int64(int32(word<<10)>>20)
			switch word & 0xffc00000 {
			case 0x02c00000: // ADDI.D
				if d != 0 {
					regs[d] = regs[j] + imm
				}
			case 0x29c00000: // ST.D
				memory[regs[j]+imm] = d
			case 0x03400000: // ANDI r0, r0, 0
				if word != 0x03400000 {
					t.Fatalf("frame %d: unexpected word %#08x", frame, word)
				}
			default:
				t.Fatalf("frame %d: unexpected word %#08x", frame, word)
			}
			sp := regs[3]
			if sp != entry && memory[sp] != 1 {
				t.Fatalf("frame %d: SP moved by %d at PC %d before r1 was stored at 0(SP)", frame, entry-sp, pc+4)
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
		if regs[3] != entry-int64(frame) {
			t.Fatalf("frame %d: SP moved by %d", frame, entry-regs[3])
		}
	}
}
