package compile

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"slices"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// Blind after addressing/immediate selection and before bank assignment and
// allocation. Decoded values are ordinary runtime values: they cannot be
// rematerialized as their original bits or folded back into literal pools.
func blindSelected(p machineProgram, target machineTarget, random io.Reader) (machineProgram, error) {
	original := p.Instructions
	p.Instructions = make([]machineInstruction, 0, len(original)*2)
	p.Values = slices.Clone(p.Values)
	p.Scopes = slices.Clone(p.Scopes)
	boundaries := make([]int, len(original)+1)
	var randomErr error
	emit := func(i machineInstruction) { p.Instructions = append(p.Instructions, i) }
	value := func(bits uint8, class backend.Class) int {
		if bits == 0 {
			bits = 64
		}
		words := uint8((int(bits) + int(target.RegisterBits()) - 1) / int(target.RegisterBits()))
		if class == backend.Vector {
			words = 1
		}
		id := len(p.Values)
		p.Values = append(p.Values, machine.SelectedValue{Bits: bits, Class: class, Words: words, Param: -1, EntrySlot: -1, EntryReg: -1})
		return id
	}
	blind := func(dst int, bits uint8, x uint64) {
		if bits == 0 {
			bits = 64
		}
		var b [8]byte
		if _, err := io.ReadFull(random, b[:]); err != nil {
			randomErr = err
			return
		}
		mask := ^uint64(0) >> (64 - bits)
		key := binary.LittleEndian.Uint64(b[:]) & mask
		a, bv := value(bits, backend.General), value(bits, backend.General)
		// Only encoded constants/keys can be rematerialized. Never decoded x.
		p.Values[a].Constant, p.Values[a].Imm = true, key
		p.Values[bv].Constant, p.Values[bv].Imm = true, (x&mask)^key
		emit(machineInstruction{Op: mConst, Dst: a, Imm: key})
		emit(machineInstruction{Op: mConst, Dst: bv, Imm: (x & mask) ^ key})
		emit(machineInstruction{Op: mBinary, Arithmetic: ir.OpXor, Bits: bits, Dst: dst, A: a, B: bv})
		p.Values[dst].Constant = false
		p.Values[dst].Imm = 0
	}
	constant := func(bits uint8, x uint64) int { id := value(bits, backend.General); blind(id, bits, x); return id }
	address := func(base, index int, scale, disp uint64, indexed bool) int {
		id := base
		w := p.Values[base].Bits
		if w == 0 {
			w = 64
		}
		if indexed {
			factor := constant(w, scale)
			product := value(w, backend.General)
			sum := value(w, backend.General)
			emit(machineInstruction{Op: mBinary, Arithmetic: ir.OpMul, Bits: w, Dst: product, A: index, B: factor})
			emit(machineInstruction{Op: mBinary, Arithmetic: ir.OpAdd, Bits: w, Dst: sum, A: base, B: product})
			id = sum
		}
		if disp != 0 {
			offset := constant(w, disp)
			sum := value(w, backend.General)
			emit(machineInstruction{Op: mBinary, Arithmetic: ir.OpAdd, Bits: w, Dst: sum, A: id, B: offset})
			id = sum
		}
		return id
	}
	for n, i := range original {
		boundaries[n] = len(p.Instructions)
		switch i.Op {
		case mConst:
			blind(i.Dst, p.Values[i.Dst].Bits, i.Imm)
		case mBinaryImmediate:
			rhs := constant(i.Bits, i.Imm)
			i.Op, i.B = mBinary, rhs
			i.Imm = 0
			emit(i)
		case mScaled:
			// Build a runtime product/sum so a selected scale/displacement cannot
			// reintroduce a chosen immediate after blinding ordinary constants.
			id := address(i.A, i.B, i.Imm, i.Disp, true)
			emit(machineInstruction{Op: mMove, Dst: i.Dst, A: id})
		case mLoadAddress, mStoreAddress:
			a := address(i.A, i.B, i.Imm, i.Disp, true)
			if i.Op == mLoadAddress {
				i.Op, i.A = mLoad64, a
			} else {
				i.Op, i.A, i.B = mStore64, a, i.C
			}
			i.Imm, i.Disp = 0, 0
			emit(i)
		case mLoadOffset, mStoreOffset:
			i.A = address(i.A, 0, 0, i.Disp, false)
			if i.Op == mLoadOffset {
				i.Op = mLoad64
			} else {
				i.Op = mStore64
			}
			i.Disp = 0
			emit(i)
		case mShift:
			i.B = constant(i.Bits, i.Imm)
			i.Op = mShiftBy
			i.Imm = 0
			emit(i)
		case mVector:
			if i.Arithmetic != ir.OpVConst {
				emit(i)
				break
			}
			key := make([]byte, len(i.Data))
			encoded := make([]byte, len(i.Data))
			if _, err := io.ReadFull(random, key); err != nil {
				randomErr = err
			}
			for k := range encoded {
				encoded[k] = i.Data[k] ^ key[k]
			}
			a, b := value(i.Bits, backend.Vector), value(i.Bits, backend.Vector)
			first, second := i, i
			first.Dst, first.Data = a, key
			second.Dst, second.Data = b, encoded
			emit(first)
			emit(second)
			i.Arithmetic, i.A, i.B, i.Data = ir.OpXor, a, b, nil
			emit(i)
		default:
			emit(i)
		}
	}
	boundaries[len(original)] = len(p.Instructions)
	for n := range p.Scopes {
		s := &p.Scopes[n]
		s.Start, s.End = boundaries[s.Start], boundaries[s.End]
	}
	if randomErr != nil {
		return machineProgram{}, fmt.Errorf("hardening randomness: %w", randomErr)
	}
	return p, nil
}

func hardenSelected(p machineProgram, target machineTarget) (machineProgram, error) {
	return blindSelected(p, target, rand.Reader)
}

// Full barriers also cover zero-length/nil inputs, for which clamping an index
// to zero alone supplies no valid element. Harden accepts a speculation
// barrier the instruction set defines or one the host must configure, and
// rejects control graphs on targets that have neither.
func hardeningBarrier(target machineTarget) (physicalInsn, error) {
	barriers, ok := target.(interface {
		Barrier(machine.Barrier) machine.BarrierContract
	})
	if !ok || barriers.Barrier(machine.BarrierSpeculation) == machine.BarrierUnavailable {
		return physicalInsn{}, fmt.Errorf("hardening control flow on %s: %w", target.Architecture(), ErrUnsupported)
	}
	return physicalInsn{Op: machine.OpBarrier, Barrier: machine.BarrierSpeculation}, nil
}

// hardenRegisters are the GPRs that hardened comparisons and clamps borrow,
// saving and restoring them. On POWER, registers 0-2 are r0, SP and TOC, and
// ISEL reads r0 as zero, so the borrowed registers start at r3.
func hardenRegisters(arch string) [3]physReg {
	if arch == "ppc64le" {
		return [3]physReg{3, 4, 5}
	}
	return [3]physReg{0, 1, 2}
}

// Control comparisons occur after allocation. Borrow three saved GPRs so decode
// temporaries and flag effects cannot overwrite live edge values. Reloads leave
// the final comparison flags intact on the supported ISAs.
func (e *physicalEmitter) blindComparison(i physicalInsn) {
	emit := e.appendRaw
	t := e.hardenRegisters
	for n, r := range t {
		emit(physicalInsn{Op: pSpill, A: r, Slot: e.hardenSlots + n})
	}
	var bytes [8]byte
	if _, err := io.ReadFull(rand.Reader, bytes[:]); err != nil {
		e.hardenErr = err
		return
	}
	key := binary.LittleEndian.Uint64(bytes[:])
	if i.Bits == 32 {
		key &= 0xffffffff
	}
	emit(physicalInsn{Op: pConst, A: t[0], Imm: key})
	emit(physicalInsn{Op: pConst, A: t[1], Imm: i.Imm ^ key})
	emit(physicalInsn{Op: pBinary, Arithmetic: ir.OpXor, Bits: i.Bits, A: t[0], B: t[0], C: t[1]})
	left := i.A
	if n := borrowed(t[:], left); n >= 0 {
		emit(physicalInsn{Op: pReload, A: t[2], Slot: e.hardenSlots + n})
		left = t[2]
	}
	emit(physicalInsn{Op: pCompareRegister, Bits: i.Bits, Comparison: i.Comparison, A: left, B: t[0]})
	for n, r := range t {
		emit(physicalInsn{Op: pReload, A: r, Slot: e.hardenSlots + n})
	}
}

// borrowed returns reg's index among the borrowed registers, or -1.
func borrowed(registers []physReg, reg physReg) int {
	for n, r := range registers {
		if r == reg {
			return n
		}
	}
	return -1
}

func clampIndex(e *physicalEmitter, arch string, bits uint8, home exportLocation, slot int) error {
	// The success edge of an unsigned-less comparison has live Below flags.
	// Select zero when the comparison fails, retaining a flag dependency even
	// on a mistrained success edge. Borrow two registers (EAX/ECX on x86)
	// without losing homes.
	emit := e.appendRaw
	t := e.hardenRegisters
	for n, r := range t[:2] {
		emit(physicalInsn{Op: pSpill, A: r, Slot: e.hardenSlots + n})
	}
	emit(physicalInsn{Op: pConst, A: t[1], Imm: 0}) // A trusted zero, preserving flags.
	words := 1
	if arch == "386" && bits == 64 {
		words = 2
	}
	saved := -1
	if home.Register >= 0 {
		saved = borrowed(t[:2], physReg(home.Register))
	}
	for word := 0; word < words; word++ {
		if home.Register >= 0 {
			if saved >= 0 {
				emit(physicalInsn{Op: pReload, A: t[0], Slot: e.hardenSlots + saved})
			} else {
				emit(physicalInsn{Op: pMove, A: t[0], B: physReg(home.Register)})
			}
		} else if slot >= 0 {
			emit(physicalInsn{Op: pReload, A: t[0], Slot: slot + word})
		} else {
			return ErrInvalidFunction
		}
		if arch == "amd64" || arch == "386" {
			code := []byte{0x0f, 0x43, 0xc1} // CMOVAE EAX,ECX
			if arch == "amd64" {
				code = append([]byte{0x48}, code...)
			}
			emit(physicalInsn{Op: machine.OpLiteralData, Data: code})
		} else {
			emit(physicalInsn{Op: machine.OpSelectFlags, A: t[0], C: t[0], D: t[1], Condition: ir.BranchBelow})
		}
		if home.Register >= 0 {
			if saved >= 0 {
				emit(physicalInsn{Op: pSpill, A: t[0], Slot: e.hardenSlots + saved})
			} else {
				emit(physicalInsn{Op: pMove, A: physReg(home.Register), B: t[0]})
			}
		} else {
			emit(physicalInsn{Op: pSpill, A: t[0], Slot: slot + word})
		}
	}
	for n, r := range t[:2] {
		emit(physicalInsn{Op: pReload, A: r, Slot: e.hardenSlots + n})
	}
	return nil
}
