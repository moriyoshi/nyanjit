#include "textflag.h"
#include "go_asm.h"

TEXT ·alignedForeignEntry(SB),NOSPLIT|NOFRAME,$0-8
	MOVV $alignedForeignC<>(SB), R4
	MOVV R4, ret+0(FP)
	RET

// C ABI entry, called by purego after runtime.cgocall. Never call Go directly
// here. Preserve the incoming SP exactly, even if it is 8 mod 16. The aligned
// 32-byte frame holds RA, original SP and the argument block pointer.
TEXT alignedForeignC<>(SB),NOSPLIT|NOFRAME,$0
	MOVV R3, R12
	ADDV $-32, R3
	MOVV $-16, R13
	AND R13, R3
	MOVV R1, 0(R3)
	MOVV R12, 8(R3)
	MOVV R4, 16(R3)
	MOVV R4, R13
	// Retain SyscallN's integer-bit-pattern mirror in the FP argument registers.
	MOVD alignedForeignCall_args+0(R13), F0
	MOVD alignedForeignCall_args+8(R13), F1
	MOVD alignedForeignCall_args+16(R13), F2
	MOVD alignedForeignCall_args+24(R13), F3
	MOVD alignedForeignCall_args+32(R13), F4
	MOVD alignedForeignCall_args+40(R13), F5
	MOVD alignedForeignCall_args+48(R13), F6
	MOVD alignedForeignCall_args+56(R13), F7
	MOVV alignedForeignCall_target(R13), R12
	MOVV alignedForeignCall_args+0(R13), R4
	MOVV alignedForeignCall_args+8(R13), R5
	MOVV alignedForeignCall_args+16(R13), R6
	MOVV alignedForeignCall_args+24(R13), R7
	MOVV alignedForeignCall_args+32(R13), R8
	MOVV alignedForeignCall_args+40(R13), R9
	MOVV alignedForeignCall_args+48(R13), R10
	MOVV alignedForeignCall_args+56(R13), R11
	JAL (R12)
	MOVV 16(R3), R13
	MOVV R4, alignedForeignCall_low(R13)
	MOVV R5, alignedForeignCall_high(R13)
	MOVV 0(R3), R1
	MOVV 8(R3), R12
	MOVV R12, R3
	RET
