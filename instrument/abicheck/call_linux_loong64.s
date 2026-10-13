#include "textflag.h"
#include "funcdata.h"

// Register file slots, 8 bytes each:
//	0..8 R23..R31 (R23 workspace)  9..16 F24..F31
//	17/18 SP before/after  19/20 g (R22) before/after
// 8(R3) keeps the register file address across the call. Nothing between
// seeding and the call may use R30, the assembler's temporary.
TEXT ·callChecked(SB),NOSPLIT,$16-48
	NO_LOCAL_POINTERS
	MOVV regs+32(FP), R19
	MOVV R19, 8(R3)
	MOVV R3, 136(R19)
	MOVV g, 152(R19)
	MOVV entry+0(FP), R20
	MOVV args+8(FP), R12
	MOVV 0(R12), R4
	MOVV 8(R12), R5
	MOVV 16(R12), R6
	MOVV 24(R12), R7
	MOVV 32(R12), R8
	MOVV 40(R12), R9
	MOVV 48(R12), R10
	MOVV 56(R12), R11
	MOVD 72(R19), F24
	MOVD 80(R19), F25
	MOVD 88(R19), F26
	MOVD 96(R19), F27
	MOVD 104(R19), F28
	MOVD 112(R19), F29
	MOVD 120(R19), F30
	MOVD 128(R19), F31
	MOVV 0(R19), R23
	MOVV 8(R19), R24
	MOVV 16(R19), R25
	MOVV 24(R19), R26
	MOVV 32(R19), R27
	MOVV 40(R19), R28
	MOVV 48(R19), R29
	MOVV 56(R19), R30
	MOVV 64(R19), R31
	JAL (R20)
	MOVV 8(R3), R19
	MOVV R23, 0(R19)
	MOVV R24, 8(R19)
	MOVV R25, 16(R19)
	MOVV R26, 24(R19)
	MOVV R27, 32(R19)
	MOVV R28, 40(R19)
	MOVV R29, 48(R19)
	MOVV R30, 56(R19)
	MOVV R31, 64(R19)
	MOVD F24, 72(R19)
	MOVD F25, 80(R19)
	MOVD F26, 88(R19)
	MOVD F27, 96(R19)
	MOVD F28, 104(R19)
	MOVD F29, 112(R19)
	MOVD F30, 120(R19)
	MOVD F31, 128(R19)
	MOVV R3, 144(R19)
	MOVV g, 160(R19)
	MOVV 152(R19), g
	MOVV R4, ret+40(FP)
	RET
