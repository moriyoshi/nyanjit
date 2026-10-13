package ppc64

import "github.com/moriyoshi/nyanjit/machine"

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
)

// Instruction is a concrete instruction selected by Emitter. A is the
// destination/data register; B and C are source/address registers. Immediate
// FP arithmetic uses A as destination, B/C as left/right, and D as the FMA
// addend. Bits selects single or double precision. VSR ordinals 0-31 alias FPRs.
// Immediate branch operands are byte displacements from PC. Symbol marks the first
// instruction of a fixed five-instruction symbolic-address construction.
type Instruction struct {
	Op                   Opcode
	A, B, C, D           machine.Register
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
