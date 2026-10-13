package amd64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func canRORX(operation op.Opcode, w uint8, p abi.ResolvedCPU) bool {
	return (operation == op.OpRotateLeft || operation == op.OpRotateRight) && (w == 32 || w == 64) && p.Has("bmi2")
}

func rorxPlan(operation op.Opcode, w uint8, d, s backend.Register, c uint8, p abi.ResolvedCPU) []byte {
	if !canRORX(operation, w, p) {
		return nil
	}
	if operation == op.OpRotateLeft {
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
