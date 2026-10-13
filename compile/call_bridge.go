package compile

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// bridgeManagedCCalls changes only the execution transition of C call sites.
// The original target and C arguments become arguments to a Go ABI helper;
// that helper uses the runtime-aware foreign call boundary.
func bridgeManagedCCalls(program *machineProgram, options compileOptions, kind emissionKind) error {
	if !options.ManagedCalls && targetArchitecture(options) != "loong64" && targetArchitecture(options) != "riscv64" {
		return nil
	}
	original := program.Instructions
	program.Instructions = nil
	positions := make([]int, len(original)+1)
	for i := range original {
		positions[i] = len(program.Instructions)
		ins := original[i]
		if ins.Op != mCallC {
			program.Instructions = append(program.Instructions, ins)
			continue
		}

		// Both LoongArch LP64D and RISC-V LP64 pass even unsigned 32-bit C
		// integers as sign-extended XLEN words.
		// Normalize before either a direct C call or the managed uintptr gateway.
		if targetArchitecture(options) == "loong64" || targetArchitecture(options) == "riscv64" {
			ins.Args = append([]int(nil), ins.Args...)
			for n, id := range ins.Args[1:] {
				if program.Values[id].Bits != 32 {
					continue
				}
				dst := len(program.Values)
				program.Values = append(program.Values, machine.SelectedValue{Bits: 64, Words: 1, Param: -1, EntrySlot: -1, EntryReg: -1})
				program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: mSignExtend, Dst: dst, A: id, Imm: 32})
				ins.Args[n+1] = dst
			}
		}
		if !options.ManagedCalls {
			program.Instructions = append(program.Instructions, ins)
			continue
		}
		count := len(ins.Args) - 1
		var address uintptr
		var symbol string
		architecture := targetArchitecture(options)
		if architecture == "386" {
			// The compiler gateway has uint64 slots, but packs the C boundary
			// into native words. Its first uint64 holds the address in the low
			// word and a bit mask of two-word arguments in the high word.
			mask := uint64(0)
			for arg, id := range ins.Args[1:] {
				if program.Values[id].Words == 2 {
					mask |= 1 << arg
				}
			}
			wideValue := func(value int) int {
				if program.Values[value].Words == 2 {
					return value
				}
				dst := len(program.Values)
				program.Values = append(program.Values, machine.SelectedValue{Bits: 64, Words: 2, Param: -1, EntrySlot: -1, EntryReg: -1})
				program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: mMove, Bits: 64, Dst: dst, A: value})
				return dst
			}
			args := append([]int(nil), ins.Args...)
			target := program.Values[args[0]]
			if target.Constant {
				args[0] = len(program.Values)
				program.Values = append(program.Values, machine.SelectedValue{Bits: 64, Words: 2, Constant: true, Imm: uint64(uint32(target.Imm)) | mask<<32, Param: -1, EntrySlot: -1, EntryReg: -1})
			} else {
				low := wideValue(args[0])
				constant := len(program.Values)
				program.Values = append(program.Values, machine.SelectedValue{Bits: 64, Words: 2, Constant: true, Imm: mask << 32, Param: -1, EntrySlot: -1, EntryReg: -1})
				args[0] = len(program.Values)
				program.Values = append(program.Values, machine.SelectedValue{Bits: 64, Words: 2, Param: -1, EntrySlot: -1, EntryReg: -1})
				program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: mBinary, Bits: 64, Arithmetic: ir.OpOr, Dst: args[0], A: low, B: constant})
			}
			for arg := 1; arg < len(args); arg++ {
				args[arg] = wideValue(args[arg])
			}
			ins.Args = args
		}
		if kind == emitText || kind == emitLayoutText {
			// Runtime gateway addresses belong to the target process. A
			// listing describes them symbolically, including on the host.
			symbol = fmt.Sprintf("nyanjit.cCallBridge%d", count)
			if architecture == "386" {
				symbol = fmt.Sprintf("nyanjit.cdeclCallBridge%d", count)
			}
		} else {
			address = cCallBridgeAddress(count)
			if architecture == "386" {
				address = cdeclCallBridgeAddress(count)
			}
			if address == 0 {
				return ErrBackendLimit
			}
		}
		id := len(program.Values)
		program.Values = append(program.Values, machine.SelectedValue{
			Words: 1, Constant: true, Imm: uint64(address), Symbol: symbol, Param: -1,
			EntrySlot: -1, EntryReg: -1,
		})
		ins.Op = mCallGo
		ins.Args = append([]int{id}, ins.Args...)
		// The helper has its own Go ABI effects. A C callee summary cannot
		// narrow the transition or the helper's register clobbers.
		ins.Effect = nil
		program.Instructions = append(program.Instructions, ins)
	}
	positions[len(original)] = len(program.Instructions)
	for i := range program.Scopes {
		scope := &program.Scopes[i]
		scope.Start, scope.End = positions[scope.Start], positions[scope.End]
	}
	return nil
}
