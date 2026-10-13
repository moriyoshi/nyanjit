package riscv64

import (
	"encoding/binary"
	"fmt"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/regalloc"
	"math"
)

func LiteralSize(ins machine.Instruction) int { return 8 }
func (e *ByteAssembler) Literal(ins machine.Instruction) error {
	if ins.Op == machine.OpLiteralData {
		if len(ins.Data)%4 != 0 {
			return machine.ErrUnsupportedOperation
		}
		if e.literalData == nil {
			e.literalData = map[int]bool{}
		}
		for i := 0; i < len(ins.Data); i += 4 {
			e.literalData[len(e.words)] = true
			e.words = append(e.words, binary.LittleEndian.Uint32(ins.Data[i:]))
		}
		return nil
	}
	if e.pc < 0 || ins.Disp > math.MaxInt64 {
		return regalloc.ErrBackendLimit
	}
	delta := int64(ins.Disp) - int64(e.pc)
	if delta < -(1<<31) || delta > (1<<31)-2049 {
		return fmt.Errorf("riscv64 literal offset exceeds AUIPC reach: %w", regalloc.ErrBackendLimit)
	}
	hi := (delta + 2048) >> 12
	lo := delta - hi*4096
	funct := uint32(3)
	if ins.Bits == 32 {
		funct = 2
	}
	e.words = append(e.words, uint32(hi)<<12|31<<7|0x17, iWord(lo, 31, funct, uint32(machine.VectorNumber(ins.A)), 0x07))
	return nil
}
