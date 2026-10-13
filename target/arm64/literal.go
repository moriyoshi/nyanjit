package arm64

import (
	"encoding/binary"
	"fmt"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/regalloc"
	"math"
)

func LiteralSize(ins machine.Instruction) int {
	if ins.Long {
		return 20
	}
	return 4
}

// The far form uses the current PC plus a signed offset, avoiding assumptions
// about the mapping's page alignment. Byte and text visitors share this plan.
func literalPlan(ins machine.Instruction, pc int) ([]vectorConcrete, error) {
	if pc < 0 || ins.Disp > math.MaxInt64 {
		return nil, regalloc.ErrBackendLimit
	}
	delta := int64(ins.Disp) - int64(pc)
	if delta%4 != 0 || !machine.IsVectorRegister(ins.A) || ins.Bits != 32 && ins.Bits != 64 && ins.Bits != 128 {
		return nil, machine.ErrUnsupportedOperation
	}
	prefix, base, load := "D", uint32(0x5c000000), uint32(0xfd400000)
	if ins.Bits == 32 {
		prefix, base, load = "S", 0x1c000000, 0xbd400000
	}
	if ins.Bits == 128 {
		prefix, base, load = "Q", 0x9c000000, 0x3dc00000
	}
	n := uint32(machine.VectorNumber(ins.A))
	if !ins.Long {
		if delta < -(1<<20) || delta >= 1<<20 {
			return nil, fmt.Errorf("arm64 literal needs a far load: %w", regalloc.ErrBackendLimit)
		}
		return []vectorConcrete{{base | uint32(delta/4)&0x7ffff<<5 | n, fmt.Sprintf("ldr %s%d, %#x # literal", prefix, n, ins.Disp)}}, nil
	}
	if delta < math.MinInt32 || delta > math.MaxInt32 {
		return nil, fmt.Errorf("arm64 literal offset exceeds signed 32 bits: %w", regalloc.ErrBackendLimit)
	}
	u := uint32(delta)
	return []vectorConcrete{
		{0x10000010, "adr X16, ."},
		{0x52800011 | (u&0xffff)<<5, fmt.Sprintf("movz W17, #%#x", u&0xffff)},
		{0x72a00011 | (u>>16)<<5, fmt.Sprintf("movk W17, #%#x, lsl #16", u>>16)},
		{0x8b31c210, "add X16, X16, W17, sxtw"},
		{load | 16<<5 | n, fmt.Sprintf("ldr %s%d, [X16] # literal %#x", prefix, n, ins.Disp)},
	}, nil
}
func (e *ByteAssembler) Literal(ins machine.Instruction) error {
	if ins.Op == machine.OpLiteralData {
		if len(ins.Data)%4 != 0 {
			return machine.ErrUnsupportedOperation
		}
		for i := 0; i < len(ins.Data); i += 4 {
			e.words = append(e.words, binary.LittleEndian.Uint32(ins.Data[i:]))
		}
		return nil
	}
	plan, err := literalPlan(ins, e.pc)
	if err != nil {
		return err
	}
	for _, i := range plan {
		e.words = append(e.words, i.word)
	}
	return nil
}
func (e *TextAssembler) Literal(ins machine.Instruction) error {
	if ins.Op == machine.OpLiteralData {
		e.lines = append(e.lines, fmt.Sprintf("%04x  .byte % x", e.pc, ins.Data))
		e.pc += len(ins.Data)
		return nil
	}
	plan, err := literalPlan(ins, e.pc)
	if err != nil {
		return err
	}
	for _, i := range plan {
		e.line("%s", i.text)
	}
	return nil
}
