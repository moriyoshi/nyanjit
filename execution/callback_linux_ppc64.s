#include "textflag.h"
#include "funcdata.h"

TEXT ·callbackNativeAddress(SB),NOSPLIT|NOFRAME,$0-8
 MOVD $·callbackNative(SB), R3
 MOVD R3, ret+0(FP)
 RET

// Shared C ABI callback on g0. Save the full C nonvolatile register set.
// Outgoing ABI0 args: 32..55; word-only callback frame: 64..143.
// All vector slots have sixteen-byte alignment.
TEXT ·callbackNative(SB),NOSPLIT|NOFRAME,$0
 MOVD LR, R0
 MOVD R0, 16(R1)
 MOVW CR, R0
 MOVD R0, 8(R1)
 MOVDU R1, -624(R1)
 MOVD R2, 24(R1)
 MOVD R11, 64(R1)
 MOVD R3, 72(R1)
 MOVD R4, 80(R1)
 MOVD R5, 88(R1)
 MOVD R6, 96(R1)
 MOVD R7, 104(R1)
 MOVD R8, 112(R1)
 MOVD R9, 120(R1)
 MOVD R10, 128(R1)
 MOVD R14, 144(R1)
 MOVD R15, 152(R1)
 MOVD R16, 160(R1)
 MOVD R17, 168(R1)
 MOVD R18, 176(R1)
 MOVD R19, 184(R1)
 MOVD R20, 192(R1)
 MOVD R21, 200(R1)
 MOVD R22, 208(R1)
 MOVD R23, 216(R1)
 MOVD R24, 224(R1)
 MOVD R25, 232(R1)
 MOVD R26, 240(R1)
 MOVD R27, 248(R1)
 MOVD R28, 256(R1)
 MOVD R29, 264(R1)
 MOVD g, 272(R1)
 MOVD R31, 280(R1)
 FMOVD F14, 288(R1)
 FMOVD F15, 296(R1)
 FMOVD F16, 304(R1)
 FMOVD F17, 312(R1)
 FMOVD F18, 320(R1)
 FMOVD F19, 328(R1)
 FMOVD F20, 336(R1)
 FMOVD F21, 344(R1)
 FMOVD F22, 352(R1)
 FMOVD F23, 360(R1)
 FMOVD F24, 368(R1)
 FMOVD F25, 376(R1)
 FMOVD F26, 384(R1)
 FMOVD F27, 392(R1)
 FMOVD F28, 400(R1)
 FMOVD F29, 408(R1)
 FMOVD F30, 416(R1)
 FMOVD F31, 424(R1)
 MOVD $432, R0
 STVX V20, (R0)(R1)
 MOVD $448, R0
 STVX V21, (R0)(R1)
 MOVD $464, R0
 STVX V22, (R0)(R1)
 MOVD $480, R0
 STVX V23, (R0)(R1)
 MOVD $496, R0
 STVX V24, (R0)(R1)
 MOVD $512, R0
 STVX V25, (R0)(R1)
 MOVD $528, R0
 STVX V26, (R0)(R1)
 MOVD $544, R0
 STVX V27, (R0)(R1)
 MOVD $560, R0
 STVX V28, (R0)(R1)
 MOVD $576, R0
 STVX V29, (R0)(R1)
 MOVD $592, R0
 STVX V30, (R0)(R1)
 MOVD $608, R0
 STVX V31, (R0)(R1)

// C may repurpose R30; pure-Go PPC64 threads do not have usable TLS.
// Find the nearest asmcgocall root through mandatory C back chains.
// The preceding 144-byte nyanjit adapter has a signature and saved g0.
 MOVBZ runtime·iscgo(SB), R3
 CMP R3, $0
 BNE callbackLoadTLS
 MOVD R1, R12
 MOVD $4096, R11
callbackFindRoot:
 MOVD 0(R12), R4
 CMP R4, $0
 BEQ callbackInvalidContext
 CMPU R4, R12
 BLE callbackInvalidContext
 MOVD 0(R4), R31
 CMP R31, $0
 BEQ callbackCheckAdapter
 MOVD R4, R12
 ADD $-1, R11
 CMP R11, $0
 BNE callbackFindRoot
 BR callbackInvalidContext
callbackCheckAdapter:
 SUB R12, R4, R31
 CMP R31, $144
 BNE callbackInvalidContext
 MOVD 136(R12), R0
 CMP R0, $0x4e59
 BNE callbackInvalidContext
 MOVD 128(R12), g
 CMP g, $0
 BEQ callbackInvalidContext

 BR callbackReady
callbackLoadTLS:
 CALL runtime·load_g(SB)
// A C-created thread can have nil g. With real cgo linked, cgocallback
// calls needAndBindM and installs the thread-local Go context itself.
callbackReady:
 XOR R0, R0
 MOVD ·ppc64CallbackDispatcher(SB), R3
 MOVD 0(R3), R3
 MOVD R3, 32(R1)
 ADD $64, R1, R4
 MOVD R4, 40(R1)
 MOVD R0, 48(R1)
 CALL runtime·cgocallback(SB)

 MOVD 136(R1), R3
 MOVD $432, R0
 LVX (R0)(R1), V20
 MOVD $448, R0
 LVX (R0)(R1), V21
 MOVD $464, R0
 LVX (R0)(R1), V22
 MOVD $480, R0
 LVX (R0)(R1), V23
 MOVD $496, R0
 LVX (R0)(R1), V24
 MOVD $512, R0
 LVX (R0)(R1), V25
 MOVD $528, R0
 LVX (R0)(R1), V26
 MOVD $544, R0
 LVX (R0)(R1), V27
 MOVD $560, R0
 LVX (R0)(R1), V28
 MOVD $576, R0
 LVX (R0)(R1), V29
 MOVD $592, R0
 LVX (R0)(R1), V30
 MOVD $608, R0
 LVX (R0)(R1), V31
 FMOVD 288(R1), F14
 FMOVD 296(R1), F15
 FMOVD 304(R1), F16
 FMOVD 312(R1), F17
 FMOVD 320(R1), F18
 FMOVD 328(R1), F19
 FMOVD 336(R1), F20
 FMOVD 344(R1), F21
 FMOVD 352(R1), F22
 FMOVD 360(R1), F23
 FMOVD 368(R1), F24
 FMOVD 376(R1), F25
 FMOVD 384(R1), F26
 FMOVD 392(R1), F27
 FMOVD 400(R1), F28
 FMOVD 408(R1), F29
 FMOVD 416(R1), F30
 FMOVD 424(R1), F31
 MOVD 144(R1), R14
 MOVD 152(R1), R15
 MOVD 160(R1), R16
 MOVD 168(R1), R17
 MOVD 176(R1), R18
 MOVD 184(R1), R19
 MOVD 192(R1), R20
 MOVD 200(R1), R21
 MOVD 208(R1), R22
 MOVD 216(R1), R23
 MOVD 224(R1), R24
 MOVD 232(R1), R25
 MOVD 240(R1), R26
 MOVD 248(R1), R27
 MOVD 256(R1), R28
 MOVD 264(R1), R29
 MOVD 272(R1), g
 MOVD 280(R1), R31
 MOVD 24(R1), R2
 ADD $624, R1
 MOVD 16(R1), R0
 MOVD R0, LR
 MOVD 8(R1), R0
 MOVW R0, CR
 RET

// Pure-Go callbacks outside the runtime-originating C gateway are unsupported.
callbackInvalidContext:
 UNDEF
