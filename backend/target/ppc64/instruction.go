package ppc64

import (
	"github.com/moriyoshi/nyanjit/backend"
)

// Opcode identifies one concrete, four-byte POWER instruction.
type Opcode uint8

const (
	Addi Opcode = iota
	Addis
	Ori
	Oris
	Xori
	Andi
	Add
	Subf
	Mulld
	And
	Or
	Xor
	Rldicl
	Rldicr
	Rlwinm
	Sradi
	Extsb
	Extsh
	Extsw
	Ld
	Std
	Ldx
	Stdx
	Ldbrx
	Stdbrx
	Lbz
	Stb
	Lhz
	Lwz
	Sth
	Stw
	Stdu
	Stdux
	Cmp
	Cmpl
	Cmpi
	Cmpli
	Isel
	B
	Bc
	Blr
	Mflr
	Mtlr
	Mtctr
	Bctrl
	Bctr
	Sc
	Neg
	Lfd
	Stfd
	Lvx
	Stvx
	Mfcr
	Mtcrf
	Mtvsrd
	Mfvsrd
	Mfvsrwz
	Xscvspdpn
	Xscvdpspn
	Xxsldwi
	Fadd
	Fsub
	Fmul
	Fdiv
	Fmadd
	Maddld
	Addc
	Subfc
	Addic
	Mfxer
	Mcrxrx
	Fsqrt
	Frsp
	Sld
	Srd
	Srad
	Slw
	Srw
	Sraw
	Rldcl
	Rlwnm
	Rlwimi
	Mulhdu
	Mulhwu
	Adde
	Subfe
	Addze
	Subfze
	Subfic
	Lfs
	Fmr
	Fneg
	Fabs
	// Sync is the heavyweight sync (hwsync): full load/store ordering.
	Sync
	// Isync discards prefetched instructions after earlier ones complete.
	Isync
	// SpeculationBarrier is ori 31,31,0, which leaves r31 unchanged. With
	// updated firmware, POWER does not execute later instructions until
	// earlier branches resolve; otherwise it is a no-op.
	SpeculationBarrier
	Divd
	Divdu
	Divw
	Divwu
	// Modsd, Modud, Modsw and Moduw are POWER9 (ISA 3.0) remainders.
	Modsd
	Modud
	Modsw
	Moduw
	Mtvsrwa
	Mtvsrwz
	// Fcfid and Fcfidu convert an FPR doubleword integer; Bits=32 selects
	// the single-precision forms fcfids and fcfidus.
	Fcfid
	Fcfidu
	Fctidz
	Fctiduz
	Fctiwz
	Fctiwuz
	// Fcmpu compares FPRs B and C into CR0; FU (bit 3) marks unordered.
	Fcmpu
	// Mulhd and Mulhw are the signed high products; mulhw leaves the upper
	// word undefined in 64-bit mode.
	Mulhd
	Mulhw
	// Cror and Crnor are the XL-form condition register logical operations
	// on CR bits 0-31: A carries BT, B carries BA and C carries BB, so
	// CR[A] = CR[B] | CR[C] (crnor: its complement). They write no GPR.
	Cror
	Crnor
)

// Instruction is a concrete instruction selected by Emitter. A is the
// destination/data register; B and C are source/address registers. Immediate
// FP arithmetic uses A as destination, B/C as left/right, and D as the FMA
// addend. Bits selects single or double precision. VSR ordinals 0-31 alias FPRs.
// Immediate branch operands are byte displacements from PC. Symbol marks the first
// instruction of a fixed five-instruction symbolic-address construction.
type Instruction struct {
	Op                   Opcode
	A, B, C, D           backend.Register
	Imm                  int64
	Shift, Mask, MaskEnd uint8
	Bits                 uint8
	BO, BI               uint8
	PC                   int
	Symbol               string
	Link                 bool // linking branch forms
	Record               bool // record forms establish CR0 LT/GT/EQ/SO
}

// Assembler visits concrete instructions without choosing expansions or PCs.
type Assembler interface {
	Visit(Instruction) error
	AssembledSize() int
}
