#include "textflag.h"
#include "go_asm.h"

TEXT ·alignedForeignEntry(SB),NOSPLIT|NOFRAME,$0-8
	MOV $alignedForeignC<>(SB), X10
	MOV X10, ret+0(FP)
	RET

// C ABI entry on g0. The original SP, RA and block pointer live in an aligned
// 32-byte frame; a C callback may reenter Go without losing this state.
TEXT alignedForeignC<>(SB),NOSPLIT|NOFRAME,$0
	MOV SP, X5
	SUB $32, SP
	AND $-16, SP
	MOV RA, 0(SP)
	MOV X5, 8(SP)
	MOV X10, 16(SP)
	MOV X10, X6
	MOVD alignedForeignCall_args+0(X6), F10
	MOVD alignedForeignCall_args+8(X6), F11
	MOVD alignedForeignCall_args+16(X6), F12
	MOVD alignedForeignCall_args+24(X6), F13
	MOVD alignedForeignCall_args+32(X6), F14
	MOVD alignedForeignCall_args+40(X6), F15
	MOVD alignedForeignCall_args+48(X6), F16
	MOVD alignedForeignCall_args+56(X6), F17
	MOV alignedForeignCall_target(X6), X5
	MOV alignedForeignCall_args+0(X6), X10
	MOV alignedForeignCall_args+8(X6), X11
	MOV alignedForeignCall_args+16(X6), X12
	MOV alignedForeignCall_args+24(X6), X13
	MOV alignedForeignCall_args+32(X6), X14
	MOV alignedForeignCall_args+40(X6), X15
	MOV alignedForeignCall_args+48(X6), X16
	MOV alignedForeignCall_args+56(X6), X17
	JALR RA, (X5)
	MOV 16(SP), X6
	MOV X10, alignedForeignCall_low(X6)
	MOV X11, alignedForeignCall_high(X6)
	MOV 0(SP), RA
	MOV 8(SP), X5
	MOV X5, SP
	RET
