#include "textflag.h"
#include "go_asm.h"

// Float-passing trampolines load XMM0–XMM7 and the convention's integer
// registers from a floatCall block and store RAX, RDX and XMM0 back.
TEXT ·callFloat(SB),NOSPLIT,$0-16
	MOVQ block+0(FP), R11
	MOVQ workspace+8(FP), R10
	MOVSD floatCall_fp+0(R11), X0
	MOVSD floatCall_fp+8(R11), X1
	MOVSD floatCall_fp+16(R11), X2
	MOVSD floatCall_fp+24(R11), X3
	MOVSD floatCall_fp+32(R11), X4
	MOVSD floatCall_fp+40(R11), X5
	MOVSD floatCall_fp+48(R11), X6
	MOVSD floatCall_fp+56(R11), X7
	MOVQ floatCall_gp+0(R11), DI
	MOVQ floatCall_gp+8(R11), SI
	MOVQ floatCall_gp+16(R11), DX
	MOVQ floatCall_gp+24(R11), CX
	MOVQ floatCall_gp+32(R11), R8
	MOVQ floatCall_gp+40(R11), R9
	MOVQ floatCall_target(R11), AX
	CALL AX
	MOVQ block+0(FP), R11
	MOVQ AX, floatCall_lo(R11)
	MOVQ DX, floatCall_hi(R11)
	MOVSD X0, floatCall_fpResult(R11)
	RET

// Go ABIInternal integer arguments are AX, BX, CX, DI, SI and R8.
TEXT ·callGoFloat(SB),NOSPLIT,$0-16
	MOVQ block+0(FP), DX
	MOVQ workspace+8(FP), R10
	MOVSD floatCall_fp+0(DX), X0
	MOVSD floatCall_fp+8(DX), X1
	MOVSD floatCall_fp+16(DX), X2
	MOVSD floatCall_fp+24(DX), X3
	MOVSD floatCall_fp+32(DX), X4
	MOVSD floatCall_fp+40(DX), X5
	MOVSD floatCall_fp+48(DX), X6
	MOVSD floatCall_fp+56(DX), X7
	MOVQ floatCall_target(DX), R11
	MOVQ floatCall_gp+0(DX), AX
	MOVQ floatCall_gp+8(DX), BX
	MOVQ floatCall_gp+16(DX), CX
	MOVQ floatCall_gp+24(DX), DI
	MOVQ floatCall_gp+32(DX), SI
	MOVQ floatCall_gp+40(DX), R8
	CALL R11
	MOVQ block+0(FP), R11
	MOVQ AX, floatCall_lo(R11)
	MOVQ DX, floatCall_hi(R11)
	MOVSD X0, floatCall_fpResult(R11)
	RET

TEXT ·floatForeignEntry(SB),NOSPLIT,$0-8
	MOVQ $floatForeignC<>(SB), AX
	MOVQ AX, ret+0(FP)
	RET

// C ABI entry, called by purego after the runtime transition with the block
// in RDI. Callee-saved RBX keeps the block and RBP the incoming RSP, which
// is realigned to 16 bytes for the call.
TEXT floatForeignC<>(SB),NOSPLIT,$0
	PUSHQ BP
	PUSHQ BX
	MOVQ SP, BP
	ANDQ $~15, SP
	MOVQ DI, BX
	MOVSD floatCall_fp+0(BX), X0
	MOVSD floatCall_fp+8(BX), X1
	MOVSD floatCall_fp+16(BX), X2
	MOVSD floatCall_fp+24(BX), X3
	MOVSD floatCall_fp+32(BX), X4
	MOVSD floatCall_fp+40(BX), X5
	MOVSD floatCall_fp+48(BX), X6
	MOVSD floatCall_fp+56(BX), X7
	MOVQ floatCall_gp+0(BX), DI
	MOVQ floatCall_gp+8(BX), SI
	MOVQ floatCall_gp+16(BX), DX
	MOVQ floatCall_gp+24(BX), CX
	MOVQ floatCall_gp+32(BX), R8
	MOVQ floatCall_gp+40(BX), R9
	MOVQ floatCall_target(BX), R11
	MOVL $8, AX // AL bounds the vector arguments of a variadic callee
	CALL R11
	MOVQ AX, floatCall_lo(BX)
	MOVQ DX, floatCall_hi(BX)
	MOVSD X0, floatCall_fpResult(BX)
	MOVQ BP, SP
	POPQ BX
	POPQ BP
	RET
