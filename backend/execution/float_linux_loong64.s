#include "textflag.h"
#include "funcdata.h"
#include "go_asm.h"

// The float-passing trampoline loads F0–F7 and R4–R11 from a floatCall
// block and stores R4, R5 and F0 back. Its frame is call's.
TEXT ·callFloat(SB),NOSPLIT,$32-16
	NO_LOCAL_POINTERS
	MOVV R1, 0(R3)
	MOVV R23, 8(R3)
	MOVV block+0(FP), R19
	MOVV workspace+8(FP), R23
	MOVD floatCall_fp+0(R19), F0
	MOVD floatCall_fp+8(R19), F1
	MOVD floatCall_fp+16(R19), F2
	MOVD floatCall_fp+24(R19), F3
	MOVD floatCall_fp+32(R19), F4
	MOVD floatCall_fp+40(R19), F5
	MOVD floatCall_fp+48(R19), F6
	MOVD floatCall_fp+56(R19), F7
	MOVV floatCall_target(R19), R20
	MOVV floatCall_gp+0(R19), R4
	MOVV floatCall_gp+8(R19), R5
	MOVV floatCall_gp+16(R19), R6
	MOVV floatCall_gp+24(R19), R7
	MOVV floatCall_gp+32(R19), R8
	MOVV floatCall_gp+40(R19), R9
	MOVV floatCall_gp+48(R19), R10
	MOVV floatCall_gp+56(R19), R11
	JAL (R20)
	MOVV 8(R3), R23
	MOVV 0(R3), R1
	MOVV block+0(FP), R19
	MOVV R4, floatCall_lo(R19)
	MOVV R5, floatCall_hi(R19)
	MOVD F0, floatCall_fpResult(R19)
	RET

TEXT ·floatForeignEntry(SB),NOSPLIT|NOFRAME,$0-8
	MOVV $floatForeignC<>(SB), R4
	MOVV R4, ret+0(FP)
	RET

// C ABI entry, as alignedForeignC: preserve the incoming SP exactly; an
// aligned 32-byte frame holds RA, the original SP and the block pointer.
TEXT floatForeignC<>(SB),NOSPLIT|NOFRAME,$0
	MOVV R3, R12
	ADDV $-32, R3
	MOVV $-16, R13
	AND R13, R3
	MOVV R1, 0(R3)
	MOVV R12, 8(R3)
	MOVV R4, 16(R3)
	MOVV R4, R13
	MOVD floatCall_fp+0(R13), F0
	MOVD floatCall_fp+8(R13), F1
	MOVD floatCall_fp+16(R13), F2
	MOVD floatCall_fp+24(R13), F3
	MOVD floatCall_fp+32(R13), F4
	MOVD floatCall_fp+40(R13), F5
	MOVD floatCall_fp+48(R13), F6
	MOVD floatCall_fp+56(R13), F7
	MOVV floatCall_target(R13), R12
	MOVV floatCall_gp+0(R13), R4
	MOVV floatCall_gp+8(R13), R5
	MOVV floatCall_gp+16(R13), R6
	MOVV floatCall_gp+24(R13), R7
	MOVV floatCall_gp+32(R13), R8
	MOVV floatCall_gp+40(R13), R9
	MOVV floatCall_gp+48(R13), R10
	MOVV floatCall_gp+56(R13), R11
	JAL (R12)
	MOVV 16(R3), R13
	MOVV R4, floatCall_lo(R13)
	MOVV R5, floatCall_hi(R13)
	MOVD F0, floatCall_fpResult(R13)
	MOVV 0(R3), R1
	MOVV 8(R3), R12
	MOVV R12, R3
	RET
