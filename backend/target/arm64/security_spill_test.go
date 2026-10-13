package arm64

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"golang.org/x/arch/arm64/arm64asm"
)

func TestSecuritySpillOffsetBoundaries(t *testing.T) {
	for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
		for _, homes := range []int{0, 9} {
			for _, slot := range []int{-1, 4085, 4086, 4095, 4096} {
				physical := slot
				if stack == abi.StackPlatform {
					physical += 1 + homes
				}
				for _, op := range []string{"spill", "reload", "save-float", "restore-float"} {
					physical = slot
					if stack == abi.StackPlatform || op == "save-float" || op == "restore-float" {
						physical += 1 + homes
					}
					t.Run(fmt.Sprint(op, "/", stack, "/", homes, "/", slot), func(t *testing.T) {
						e := NewByteAssembler(stack, homes)
						wantOp := arm64asm.STR
						switch op {
						case "spill":
							e.Spill(7, slot)
						case "reload":
							e.Reload(7, slot)
							wantOp = arm64asm.LDR
						case "save-float":
							e.SaveCFloat(7, slot)
						case "restore-float":
							e.RestoreCFloat(7, slot)
							wantOp = arm64asm.LDR
						}
						raw := e.Bytes()
						if slot < 0 || physical > 4095 {
							if len(raw) != 0 {
								t.Fatalf("out-of-range spill yielded %x", raw)
							}
							return
						}
						if len(raw) != 4 {
							t.Fatalf("spill bytes %x", raw)
						}
						ins, err := arm64asm.Decode(raw)
						if err != nil || ins.Op != wantOp {
							t.Fatalf("spill changed opcode: %v %v", ins, err)
						}
						if got := int(binary.LittleEndian.Uint32(raw)>>10) & 4095; got != physical {
							t.Fatalf("spill offset %d want %d", got*8, physical*8)
						}
					})
				}
			}
		}
	}
}
