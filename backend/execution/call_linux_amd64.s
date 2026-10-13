#include "textflag.h"

TEXT ·call(SB),NOSPLIT,$0-32
	MOVQ address+0(FP), AX
	MOVQ args+8(FP), R11
	MOVQ workspace+16(FP), R10
	MOVQ 0(R11), DI
	MOVQ 8(R11), SI
	MOVQ 16(R11), DX
	MOVQ 24(R11), CX
	MOVQ 32(R11), R8
	MOVQ 40(R11), R9
	CALL AX
	MOVQ AX, ret+24(FP)
	RET

// C ABI integer pair result: low in AX and high in DX.
TEXT ·callPair(SB),NOSPLIT,$0-40
	MOVQ address+0(FP), AX
	MOVQ args+8(FP), R11
	MOVQ workspace+16(FP), R10
	MOVQ 0(R11), DI
	MOVQ 8(R11), SI
	MOVQ 16(R11), DX
	MOVQ 24(R11), CX
	MOVQ 32(R11), R8
	MOVQ 40(R11), R9
	CALL AX
	MOVQ AX, ret+24(FP)
	MOVQ DX, ret1+32(FP)
	RET

// Go ABIInternal integer arguments for this builder's six-argument limit are
// AX, BX, CX, DI, SI, and R8. R10 supplies the hidden spill-area pointer.
TEXT ·callGo(SB),NOSPLIT,$0-32
	MOVQ address+0(FP), R11
	MOVQ args+8(FP), DX
	MOVQ 0(DX), AX
	MOVQ 8(DX), BX
	MOVQ 16(DX), CX
	MOVQ 24(DX), DI
	MOVQ 32(DX), SI
	MOVQ 40(DX), R8
	MOVQ workspace+16(FP), R10
	CALL R11
	MOVQ AX, ret+24(FP)
	RET
