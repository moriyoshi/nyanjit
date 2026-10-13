package amd64

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func canRORX(op ir.Opcode, w uint8, p abi.CPUProfile) bool {
	return p.Has("bmi2") && (w == 32 || w == 64) && (op == ir.OpRotateLeft || op == ir.OpRotateRight)
}

func rorxPlan(op ir.Opcode, w uint8, d, s machine.Register, c uint8, p abi.CPUProfile) []byte {
	if !canRORX(op, w, p) {
		return nil
	}
	if op == ir.OpRotateLeft {
		c = (w - c) % w
	}
	b2 := byte(0xe3)
	if d >= 8 {
		b2 &^= 0x80
	}
	if s >= 8 {
		b2 &^= 0x20
	}
	b3 := byte(0x7b)
	if w == 64 {
		b3 |= 0x80
	}
	return []byte{0xc4, b2, b3, 0xf0, 0xc0 | byte(d&7)<<3 | byte(s&7), c}
}
