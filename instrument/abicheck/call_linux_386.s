#include "textflag.h"
#include "funcdata.h"

// Register file slots, 4 bytes each:
//	0 EBX  1 ESI  2 EDI  3 EBP (workspace)
//	4/5 SP before/after  6/7 g (TLS slot) before/after
// Like execution's call386 and callGo386, sixteen outgoing argument words
// occupy 0(SP)..63(SP); a Go result occupies the two words that follow the
// entry's arguments.
TEXT ·callChecked(SB),NOSPLIT,$72-24
	NO_LOCAL_POINTERS
	MOVL args+4(FP), BX
	MOVL 0(BX), CX
	MOVL CX, 0(SP)
	MOVL 4(BX), CX
	MOVL CX, 4(SP)
	MOVL 8(BX), CX
	MOVL CX, 8(SP)
	MOVL 12(BX), CX
	MOVL CX, 12(SP)
	MOVL 16(BX), CX
	MOVL CX, 16(SP)
	MOVL 20(BX), CX
	MOVL CX, 20(SP)
	MOVL 24(BX), CX
	MOVL CX, 24(SP)
	MOVL 28(BX), CX
	MOVL CX, 28(SP)
	MOVL 32(BX), CX
	MOVL CX, 32(SP)
	MOVL 36(BX), CX
	MOVL CX, 36(SP)
	MOVL 40(BX), CX
	MOVL CX, 40(SP)
	MOVL 44(BX), CX
	MOVL CX, 44(SP)
	MOVL 48(BX), CX
	MOVL CX, 48(SP)
	MOVL 52(BX), CX
	MOVL CX, 52(SP)
	MOVL 56(BX), CX
	MOVL CX, 56(SP)
	MOVL 60(BX), CX
	MOVL CX, 60(SP)
	MOVL regs+16(FP), AX
	MOVL SP, 16(AX)
	MOVL TLS, CX
	MOVL 0(CX)(TLS*1), CX
	MOVL CX, 24(AX)
	MOVL 0(AX), BX
	MOVL 4(AX), SI
	MOVL 8(AX), DI
	MOVL 12(AX), BP
	MOVL entry+0(FP), AX
	CALL AX
	MOVL regs+16(FP), CX
	MOVL BX, 0(CX)
	MOVL SI, 4(CX)
	MOVL DI, 8(CX)
	MOVL BP, 12(CX)
	MOVL SP, 20(CX)
	MOVL TLS, BX
	MOVL 0(BX)(TLS*1), BX
	MOVL BX, 28(CX)
	CMPL goABI+8(FP), $0
	JEQ done
	MOVL words+12(FP), CX
	MOVL 0(SP)(CX*4), AX
done:
	MOVL AX, ret+20(FP)
	RET
