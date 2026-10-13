#include "textflag.h"
#include "funcdata.h"

// Register file slots, 8 bytes each:
//	0 X8 (workspace)  1 X18  2..10 X19..X27  11 X29  12..19 D8..D15
//	20/21 SP before/after  22/23 g (X28) before/after
// The frame makes the assembler save and restore LR and the caller's X29.
// Go keeps that X29 just below SP, where an AAPCS64 callee may build its own
// frame, so the call runs 16 bytes lower. The gap's first word repeats the
// saved LR, where Go's unwinder looks for this frame's return address.
TEXT ·callChecked(SB),NOSPLIT,$16-48
	NO_LOCAL_POINTERS
	MOVD regs+32(FP), R10
	MOVD RSP, R11
	MOVD R11, 160(R10)
	MOVD g, 176(R10)
	MOVD entry+0(FP), R9
	MOVD args+8(FP), R11
	MOVD 0(R11), R0
	MOVD 8(R11), R1
	MOVD 16(R11), R2
	MOVD 24(R11), R3
	MOVD 32(R11), R4
	MOVD 40(R11), R5
	MOVD 48(R11), R6
	MOVD 56(R11), R7
	FMOVD 96(R10), F8
	FMOVD 104(R10), F9
	FMOVD 112(R10), F10
	FMOVD 120(R10), F11
	FMOVD 128(R10), F12
	FMOVD 136(R10), F13
	FMOVD 144(R10), F14
	FMOVD 152(R10), F15
	MOVD 0(R10), R8
	MOVD 8(R10), R18_PLATFORM
	MOVD 16(R10), R19
	MOVD 24(R10), R20
	MOVD 32(R10), R21
	MOVD 40(R10), R22
	MOVD 48(R10), R23
	MOVD 56(R10), R24
	MOVD 64(R10), R25
	MOVD 72(R10), R26
	MOVD 80(R10), R27
	MOVD 88(R10), R29
	SUB $16, RSP
	MOVD R30, 0(RSP)
	CALL (R9)
	ADD $16, RSP
	MOVD regs+32(FP), R10
	MOVD R8, 0(R10)
	MOVD R18_PLATFORM, 8(R10)
	MOVD R19, 16(R10)
	MOVD R20, 24(R10)
	MOVD R21, 32(R10)
	MOVD R22, 40(R10)
	MOVD R23, 48(R10)
	MOVD R24, 56(R10)
	MOVD R25, 64(R10)
	MOVD R26, 72(R10)
	MOVD R27, 80(R10)
	MOVD R29, 88(R10)
	FMOVD F8, 96(R10)
	FMOVD F9, 104(R10)
	FMOVD F10, 112(R10)
	FMOVD F11, 120(R10)
	FMOVD F12, 128(R10)
	FMOVD F13, 136(R10)
	FMOVD F14, 144(R10)
	FMOVD F15, 152(R10)
	MOVD RSP, R11
	MOVD R11, 168(R10)
	MOVD g, 184(R10)
	MOVD 176(R10), g
	MOVD R0, ret+40(FP)
	RET
