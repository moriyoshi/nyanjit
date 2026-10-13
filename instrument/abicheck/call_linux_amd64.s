#include "textflag.h"
#include "funcdata.h"

// Register file slots, 8 bytes each:
//	0 RBX  1 RBP  2 R12  3 R13  4 R15  5 R10 (workspace)  6 X15 (zero)
//	7/8 SP before/after  9/10 g (R14) before/after
// The frame makes the assembler save and restore the caller's BP. Arguments
// are loaded after seeding because the Go order passes the second in RBX.
TEXT ·callChecked(SB),NOSPLIT,$8-48
	NO_LOCAL_POINTERS
	MOVQ regs+32(FP), AX
	MOVQ SP, 56(AX)
	MOVQ R14, 72(AX)
	MOVQ 0(AX), BX
	MOVQ 8(AX), BP
	MOVQ 16(AX), R12
	MOVQ 24(AX), R13
	MOVQ 32(AX), R15
	MOVQ 40(AX), R10
	MOVQ 48(AX), X15
	MOVQ entry+0(FP), R11
	CMPQ goABI+16(FP), $0
	JNE goArgs
	MOVQ args+8(FP), AX
	MOVQ 0(AX), DI
	MOVQ 8(AX), SI
	MOVQ 16(AX), DX
	MOVQ 24(AX), CX
	MOVQ 32(AX), R8
	MOVQ 40(AX), R9
	JMP call
goArgs:
	MOVQ args+8(FP), DX
	MOVQ 0(DX), AX
	MOVQ 8(DX), BX
	MOVQ 16(DX), CX
	MOVQ 24(DX), DI
	MOVQ 32(DX), SI
	MOVQ 40(DX), R8
call:
	CALL R11
	MOVQ AX, ret+40(FP)
	MOVQ regs+32(FP), AX
	MOVQ BX, 0(AX)
	MOVQ BP, 8(AX)
	MOVQ R12, 16(AX)
	MOVQ R13, 24(AX)
	MOVQ R15, 32(AX)
	MOVQ R10, 40(AX)
	MOVQ X15, 48(AX)
	MOVQ SP, 64(AX)
	MOVQ R14, 80(AX)
	MOVQ 72(AX), R14
	XORPS X15, X15
	RET
