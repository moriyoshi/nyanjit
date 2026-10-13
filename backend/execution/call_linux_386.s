#include "textflag.h"
#include "funcdata.h"

// call386 enters a 32-bit C ABI function from a fixed, runtime-described Go
// frame. Sixteen outgoing argument words occupy 0(SP)..63(SP); saved registers
// occupy the final 16 bytes. A fixed frame lets GC unwind through this call.
TEXT ·call386(SB),NOSPLIT,$80-20
	NO_LOCAL_POINTERS
	MOVL BX, 64(SP)
	MOVL SI, 68(SP)
	MOVL DI, 72(SP)
	MOVL BP, 76(SP)
	MOVL address+0(FP), AX
	MOVL args+4(FP), BX
	MOVL workspace+8(FP), BP
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
	CALL AX
	MOVL 76(SP), BP
	MOVL 72(SP), DI
	MOVL 68(SP), SI
	MOVL 64(SP), BX
	MOVL AX, lo+12(FP)
	MOVL DX, hi+16(FP)
	RET

// callGo386 calls a 386 Go ABI function. Its two-word result slot directly
// follows the argument words. EBP supplies workspace for unregistered leaf
// entries. SP remains fixed and visible to the Go unwinder.
TEXT ·callGo386(SB),NOSPLIT,$88-24
	NO_LOCAL_POINTERS
	MOVL BX, 72(SP)
	MOVL SI, 76(SP)
	MOVL DI, 80(SP)
	MOVL BP, 84(SP)
	MOVL address+0(FP), AX
	MOVL args+4(FP), BX
	MOVL workspace+8(FP), BP
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
	CALL AX
	MOVL words+12(FP), CX
	SHLL $2, CX
	MOVL SP, BX
	ADDL CX, BX
	MOVL 0(BX), AX
	MOVL 4(BX), DX
	MOVL 84(SP), BP
	MOVL 80(SP), DI
	MOVL 76(SP), SI
	MOVL 72(SP), BX
	MOVL AX, lo+16(FP)
	MOVL DX, hi+20(FP)
	RET
