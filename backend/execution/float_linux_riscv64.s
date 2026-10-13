#include "textflag.h"
#include "funcdata.h"
#include "go_asm.h"

// The float-passing trampoline loads F10–F17 and X10–X17 from a floatCall
// block and stores X10, X11 and F10 back. Its frame is call's.
TEXT ·callFloat(SB),NOSPLIT,$32-16
	NO_LOCAL_POINTERS
	MOV RA, 0(SP)
	MOV X9, 8(SP)
	MOV block+0(FP), X30
	MOV workspace+8(FP), X9
	MOVD floatCall_fp+0(X30), F10
	MOVD floatCall_fp+8(X30), F11
	MOVD floatCall_fp+16(X30), F12
	MOVD floatCall_fp+24(X30), F13
	MOVD floatCall_fp+32(X30), F14
	MOVD floatCall_fp+40(X30), F15
	MOVD floatCall_fp+48(X30), F16
	MOVD floatCall_fp+56(X30), F17
	MOV floatCall_target(X30), X31
	MOV floatCall_gp+0(X30), X10
	MOV floatCall_gp+8(X30), X11
	MOV floatCall_gp+16(X30), X12
	MOV floatCall_gp+24(X30), X13
	MOV floatCall_gp+32(X30), X14
	MOV floatCall_gp+40(X30), X15
	MOV floatCall_gp+48(X30), X16
	MOV floatCall_gp+56(X30), X17
	JALR RA, (X31)
	MOV 8(SP), X9
	MOV 0(SP), RA
	MOV block+0(FP), X30
	MOV X10, floatCall_lo(X30)
	MOV X11, floatCall_hi(X30)
	MOVD F10, floatCall_fpResult(X30)
	RET

TEXT ·floatForeignEntry(SB),NOSPLIT|NOFRAME,$0-8
	MOV $floatForeignC<>(SB), X10
	MOV X10, ret+0(FP)
	RET

// C ABI entry on g0, as alignedForeignC: an aligned 32-byte frame holds RA,
// the original SP and the block pointer.
TEXT floatForeignC<>(SB),NOSPLIT|NOFRAME,$0
	MOV SP, X5
	SUB $32, SP
	AND $-16, SP
	MOV RA, 0(SP)
	MOV X5, 8(SP)
	MOV X10, 16(SP)
	MOV X10, X6
	MOVD floatCall_fp+0(X6), F10
	MOVD floatCall_fp+8(X6), F11
	MOVD floatCall_fp+16(X6), F12
	MOVD floatCall_fp+24(X6), F13
	MOVD floatCall_fp+32(X6), F14
	MOVD floatCall_fp+40(X6), F15
	MOVD floatCall_fp+48(X6), F16
	MOVD floatCall_fp+56(X6), F17
	MOV floatCall_target(X6), X5
	MOV floatCall_gp+0(X6), X10
	MOV floatCall_gp+8(X6), X11
	MOV floatCall_gp+16(X6), X12
	MOV floatCall_gp+24(X6), X13
	MOV floatCall_gp+32(X6), X14
	MOV floatCall_gp+40(X6), X15
	MOV floatCall_gp+48(X6), X16
	MOV floatCall_gp+56(X6), X17
	JALR RA, (X5)
	MOV 16(SP), X6
	MOV X10, floatCall_lo(X6)
	MOV X11, floatCall_hi(X6)
	MOVD F10, floatCall_fpResult(X6)
	MOV 0(SP), RA
	MOV 8(SP), X5
	MOV X5, SP
	RET
