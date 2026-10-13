//go:build linux && ppc64le

#include "textflag.h"
#include "funcdata.h"
#include "go_asm.h"

// The float-passing trampoline loads F1–F8 and R3–R10 from a floatCall
// block and stores R3, R4 and F1 back. Its frame and 16-byte call alignment
// are call's.
TEXT ·callFloat(SB),NOSPLIT,$112-16
	NO_LOCAL_POINTERS
	MOVD R29, 112(R1)
	MOVD R2, 120(R1)
	MOVD block+0(FP), R11
	MOVD workspace+8(FP), R29
	FMOVD floatCall_fp+0(R11), F1
	FMOVD floatCall_fp+8(R11), F2
	FMOVD floatCall_fp+16(R11), F3
	FMOVD floatCall_fp+24(R11), F4
	FMOVD floatCall_fp+32(R11), F5
	FMOVD floatCall_fp+40(R11), F6
	FMOVD floatCall_fp+48(R11), F7
	FMOVD floatCall_fp+56(R11), F8
	MOVD floatCall_target(R11), R12
	MOVD floatCall_gp+0(R11), R3
	MOVD floatCall_gp+8(R11), R4
	MOVD floatCall_gp+16(R11), R5
	MOVD floatCall_gp+24(R11), R6
	MOVD floatCall_gp+32(R11), R7
	MOVD floatCall_gp+40(R11), R8
	MOVD floatCall_gp+48(R11), R9
	MOVD floatCall_gp+56(R11), R10
	XOR R0, R0
	MOVD R12, CTR
	ANDCC $15, R1, R11
	BEQ floatAligned
	MOVD 0(R1), R11
	MOVD R11, -8(R1)
	ADD $-8, R1
	BL (CTR)
	ADD $8, R1
	BR floatDone
floatAligned:
	BL (CTR)
floatDone:
	XOR R0, R0
	MOVD 112(R1), R29
	MOVD 120(R1), R2
	MOVD block+0(FP), R11
	MOVD R3, floatCall_lo(R11)
	MOVD R4, floatCall_hi(R11)
	FMOVD F1, floatCall_fpResult(R11)
	RET

TEXT ·floatForeignEntry(SB),NOSPLIT|NOFRAME,$0-8
	MOVD $floatForeignC<>(SB), R3
	MOVD R3, ret+0(FP)
	RET

// ELFv2 C entry, called by purego after the runtime transition with the
// block in R3. A 64-byte frame saves the TOC and callee-saved R14, which
// keeps the block; LR goes to the caller's save slot.
TEXT floatForeignC<>(SB),NOSPLIT|NOFRAME,$0
	MOVD LR, R0
	MOVD R0, 16(R1)
	MOVDU R1, -64(R1)
	MOVD R2, 24(R1)
	MOVD R14, 48(R1)
	MOVD R3, R14
	FMOVD floatCall_fp+0(R14), F1
	FMOVD floatCall_fp+8(R14), F2
	FMOVD floatCall_fp+16(R14), F3
	FMOVD floatCall_fp+24(R14), F4
	FMOVD floatCall_fp+32(R14), F5
	FMOVD floatCall_fp+40(R14), F6
	FMOVD floatCall_fp+48(R14), F7
	FMOVD floatCall_fp+56(R14), F8
	MOVD floatCall_target(R14), R12
	MOVD floatCall_gp+0(R14), R3
	MOVD floatCall_gp+8(R14), R4
	MOVD floatCall_gp+16(R14), R5
	MOVD floatCall_gp+24(R14), R6
	MOVD floatCall_gp+32(R14), R7
	MOVD floatCall_gp+40(R14), R8
	MOVD floatCall_gp+48(R14), R9
	MOVD floatCall_gp+56(R14), R10
	MOVD R12, CTR
	BL (CTR)
	MOVD 24(R1), R2
	MOVD R3, floatCall_lo(R14)
	MOVD R4, floatCall_hi(R14)
	FMOVD F1, floatCall_fpResult(R14)
	MOVD 48(R1), R14
	ADD $64, R1
	MOVD 16(R1), R0
	MOVD R0, LR
	RET
