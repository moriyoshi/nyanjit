#include "textflag.h"
#include "funcdata.h"
#include "go_asm.h"

// Float-passing trampolines load V0–V7 and X0–X7 from a floatCall block and
// store X0, X1 and D0 back. Like call, unmanaged code runs 16 bytes lower.
TEXT ·callFloat(SB),NOSPLIT,$0-16
	NO_LOCAL_POINTERS
	MOVD block+0(FP), R10
	MOVD workspace+8(FP), R8
	FMOVD floatCall_fp+0(R10), F0
	FMOVD floatCall_fp+8(R10), F1
	FMOVD floatCall_fp+16(R10), F2
	FMOVD floatCall_fp+24(R10), F3
	FMOVD floatCall_fp+32(R10), F4
	FMOVD floatCall_fp+40(R10), F5
	FMOVD floatCall_fp+48(R10), F6
	FMOVD floatCall_fp+56(R10), F7
	MOVD floatCall_gp+0(R10), R0
	MOVD floatCall_gp+8(R10), R1
	MOVD floatCall_gp+16(R10), R2
	MOVD floatCall_gp+24(R10), R3
	MOVD floatCall_gp+32(R10), R4
	MOVD floatCall_gp+40(R10), R5
	MOVD floatCall_gp+48(R10), R6
	MOVD floatCall_gp+56(R10), R7
	MOVD floatCall_target(R10), R9
	SUB $16, RSP
	MOVD R30, 0(RSP)
	CALL (R9)
	ADD $16, RSP
	MOVD block+0(FP), R10
	MOVD R0, floatCall_lo(R10)
	MOVD R1, floatCall_hi(R10)
	FMOVD F0, floatCall_fpResult(R10)
	RET

// Go-managed code is entered at this frame's SP with no gap, as callManaged.
TEXT ·callManagedFloat(SB),NOSPLIT,$0-16
	NO_LOCAL_POINTERS
	MOVD block+0(FP), R10
	MOVD workspace+8(FP), R8
	FMOVD floatCall_fp+0(R10), F0
	FMOVD floatCall_fp+8(R10), F1
	FMOVD floatCall_fp+16(R10), F2
	FMOVD floatCall_fp+24(R10), F3
	FMOVD floatCall_fp+32(R10), F4
	FMOVD floatCall_fp+40(R10), F5
	FMOVD floatCall_fp+48(R10), F6
	FMOVD floatCall_fp+56(R10), F7
	MOVD floatCall_gp+0(R10), R0
	MOVD floatCall_gp+8(R10), R1
	MOVD floatCall_gp+16(R10), R2
	MOVD floatCall_gp+24(R10), R3
	MOVD floatCall_gp+32(R10), R4
	MOVD floatCall_gp+40(R10), R5
	MOVD floatCall_gp+48(R10), R6
	MOVD floatCall_gp+56(R10), R7
	MOVD floatCall_target(R10), R9
	CALL (R9)
	MOVD block+0(FP), R10
	MOVD R0, floatCall_lo(R10)
	MOVD R1, floatCall_hi(R10)
	FMOVD F0, floatCall_fpResult(R10)
	RET

TEXT ·floatForeignEntry(SB),NOSPLIT|NOFRAME,$0-8
	MOVD $floatForeignC<>(SB), R0
	MOVD R0, ret+0(FP)
	RET

// C ABI entry, called by purego after the runtime transition with the block
// in X0. Callee-saved X19 keeps the block across the call.
TEXT floatForeignC<>(SB),NOSPLIT|NOFRAME,$0
	SUB $32, RSP
	MOVD R29, 0(RSP)
	MOVD R30, 8(RSP)
	MOVD R19, 16(RSP)
	MOVD R0, R19
	FMOVD floatCall_fp+0(R19), F0
	FMOVD floatCall_fp+8(R19), F1
	FMOVD floatCall_fp+16(R19), F2
	FMOVD floatCall_fp+24(R19), F3
	FMOVD floatCall_fp+32(R19), F4
	FMOVD floatCall_fp+40(R19), F5
	FMOVD floatCall_fp+48(R19), F6
	FMOVD floatCall_fp+56(R19), F7
	MOVD floatCall_gp+0(R19), R0
	MOVD floatCall_gp+8(R19), R1
	MOVD floatCall_gp+16(R19), R2
	MOVD floatCall_gp+24(R19), R3
	MOVD floatCall_gp+32(R19), R4
	MOVD floatCall_gp+40(R19), R5
	MOVD floatCall_gp+48(R19), R6
	MOVD floatCall_gp+56(R19), R7
	MOVD floatCall_target(R19), R9
	CALL (R9)
	MOVD R0, floatCall_lo(R19)
	MOVD R1, floatCall_hi(R19)
	FMOVD F0, floatCall_fpResult(R19)
	MOVD 16(RSP), R19
	MOVD 8(RSP), R30
	MOVD 0(RSP), R29
	ADD $32, RSP
	RET
