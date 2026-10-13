#include "textflag.h"
#include "funcdata.h"
#include "go_asm.h"

// call386Float is call386 for a cdecl entry with a float result in x87
// ST(0): it copies the block's sixteen stack words, then pops ST(0) into
// the block as a binary32 or binary64 (block bits) beside EAX and EDX.
TEXT ·call386Float(SB),NOSPLIT,$80-8
	NO_LOCAL_POINTERS
	MOVL BX, 64(SP)
	MOVL SI, 68(SP)
	MOVL DI, 72(SP)
	MOVL BP, 76(SP)
	MOVL block+0(FP), BX
	MOVL workspace+4(FP), BP
	MOVL floatCall_gp+0(BX), CX
	MOVL CX, 0(SP)
	MOVL floatCall_gp+4(BX), CX
	MOVL CX, 4(SP)
	MOVL floatCall_gp+8(BX), CX
	MOVL CX, 8(SP)
	MOVL floatCall_gp+12(BX), CX
	MOVL CX, 12(SP)
	MOVL floatCall_gp+16(BX), CX
	MOVL CX, 16(SP)
	MOVL floatCall_gp+20(BX), CX
	MOVL CX, 20(SP)
	MOVL floatCall_gp+24(BX), CX
	MOVL CX, 24(SP)
	MOVL floatCall_gp+28(BX), CX
	MOVL CX, 28(SP)
	MOVL floatCall_gp+32(BX), CX
	MOVL CX, 32(SP)
	MOVL floatCall_gp+36(BX), CX
	MOVL CX, 36(SP)
	MOVL floatCall_gp+40(BX), CX
	MOVL CX, 40(SP)
	MOVL floatCall_gp+44(BX), CX
	MOVL CX, 44(SP)
	MOVL floatCall_gp+48(BX), CX
	MOVL CX, 48(SP)
	MOVL floatCall_gp+52(BX), CX
	MOVL CX, 52(SP)
	MOVL floatCall_gp+56(BX), CX
	MOVL CX, 56(SP)
	MOVL floatCall_gp+60(BX), CX
	MOVL CX, 60(SP)
	MOVL floatCall_target(BX), AX
	CALL AX
	MOVL block+0(FP), BX
	MOVL AX, floatCall_lo(BX)
	MOVL DX, floatCall_hi(BX)
	MOVL floatCall_bits(BX), CX
	CMPL CX, $32
	JEQ single
	FMOVDP F0, floatCall_fpResult(BX)
	JMP done
single:
	FMOVFP F0, floatCall_fpResult(BX)
done:
	MOVL 76(SP), BP
	MOVL 72(SP), DI
	MOVL 68(SP), SI
	MOVL 64(SP), BX
	RET

TEXT ·floatForeignEntry(SB),NOSPLIT,$0-4
	MOVL $floatForeignC<>(SB), AX
	MOVL AX, ret+0(FP)
	RET

// cdecl entry, called by purego after the runtime transition with the
// block address at 4(SP). EBX keeps the block and EBP the incoming ESP; the
// sixteen stack words are copied to a 16-byte aligned area. A float result
// is popped from ST(0) when block bits is 32 or 64.
TEXT floatForeignC<>(SB),NOSPLIT,$0
	PUSHL BP
	MOVL SP, BP
	PUSHL BX
	MOVL 8(BP), BX
	SUBL $64, SP
	ANDL $~15, SP
	MOVL floatCall_gp+0(BX), CX
	MOVL CX, 0(SP)
	MOVL floatCall_gp+4(BX), CX
	MOVL CX, 4(SP)
	MOVL floatCall_gp+8(BX), CX
	MOVL CX, 8(SP)
	MOVL floatCall_gp+12(BX), CX
	MOVL CX, 12(SP)
	MOVL floatCall_gp+16(BX), CX
	MOVL CX, 16(SP)
	MOVL floatCall_gp+20(BX), CX
	MOVL CX, 20(SP)
	MOVL floatCall_gp+24(BX), CX
	MOVL CX, 24(SP)
	MOVL floatCall_gp+28(BX), CX
	MOVL CX, 28(SP)
	MOVL floatCall_gp+32(BX), CX
	MOVL CX, 32(SP)
	MOVL floatCall_gp+36(BX), CX
	MOVL CX, 36(SP)
	MOVL floatCall_gp+40(BX), CX
	MOVL CX, 40(SP)
	MOVL floatCall_gp+44(BX), CX
	MOVL CX, 44(SP)
	MOVL floatCall_gp+48(BX), CX
	MOVL CX, 48(SP)
	MOVL floatCall_gp+52(BX), CX
	MOVL CX, 52(SP)
	MOVL floatCall_gp+56(BX), CX
	MOVL CX, 56(SP)
	MOVL floatCall_gp+60(BX), CX
	MOVL CX, 60(SP)
	MOVL floatCall_target(BX), AX
	CALL AX
	MOVL AX, floatCall_lo(BX)
	MOVL DX, floatCall_hi(BX)
	MOVL floatCall_bits(BX), CX
	CMPL CX, $64
	JNE foreignSingle
	FMOVDP F0, floatCall_fpResult(BX)
	JMP foreignDone
foreignSingle:
	CMPL CX, $32
	JNE foreignDone
	FMOVFP F0, floatCall_fpResult(BX)
foreignDone:
	LEAL -4(BP), SP
	POPL BX
	POPL BP
	RET
