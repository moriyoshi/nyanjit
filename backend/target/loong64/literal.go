package loong64

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// Literal loads always occupy two words, so pools and branches share a stable
// layout. PCADDU12I uses the instruction PC, not its page boundary.
func (e *ByteAssembler) Literal(i machine.Instruction) error {
	if !e.require(i.Op == machine.OpLiteralLoad || i.Op == machine.OpLiteralData) {
		return e.err
	}
	if !e.require((validInstructionRegisters(i))) {
		return e.err
	}

	if i.Op == machine.OpLiteralData {
		if len(i.Data)%4 != 0 {
			return machine.ErrUnsupportedOperation
		}
		for n := 0; n < len(i.Data); n += 4 {
			w := binary.LittleEndian.Uint32(i.Data[n:])
			e.emit(w, ".byte 0x%02x, 0x%02x, 0x%02x, 0x%02x", byte(w), byte(w>>8), byte(w>>16), byte(w>>24))
		}
		return nil
	}
	if e.pc < 0 || i.Disp > math.MaxInt64 {
		return backend.ErrBackendLimit
	}
	delta := int64(i.Disp) - int64(e.pc)
	if delta < -(1<<31) || delta > (1<<31)-2049 {
		return fmt.Errorf("loong64 literal exceeds PCADDU12I reach: %w", backend.ErrBackendLimit)
	}
	hi := (delta + 2048) >> 12
	lo := delta - hi*4096
	e.upper(0x1c000000, "pcaddu12i", 19, uint64(hi))
	op, name := uint32(0x2b800000), "fld.d"
	if i.Bits == 32 {
		op, name = 0x2b000000, "fld.s"
	}
	if i.Bits == 128 {
		e.emit(0x2c000000|(uint32(lo)&4095)<<10|19<<5|uint32(backend.VectorNumber(i.A)), "vld vr%d, %d(r19)", backend.VectorNumber(i.A), lo)
		return nil
	}
	e.emit(op|(uint32(lo)&4095)<<10|19<<5|uint32(backend.VectorNumber(i.A)), "%s f%d, %d(r19)", name, backend.VectorNumber(i.A), lo)
	return nil
}
