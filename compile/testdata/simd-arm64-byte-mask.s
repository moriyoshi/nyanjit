0000  ldr Q0, [X0]
0004  ubfm X1, X1, #0, #7
0008  dup V1.16B, W1
000c  cmeq V0.16B, V0.16B, V1.16B
0010  movz X16, #513, lsl #0
0014  movk X16, #2052, lsl #16
0018  movk X16, #8208, lsl #32
001c  movk X16, #32832, lsl #48
0020  dup V30.2D, X16
0024  and V30.16B, V0.16B, V30.16B
0028  ext V31.16B, V30.16B, V30.16B, #8
002c  zip1 V30.16B, V30.16B, V31.16B
0030  addv H30, V30.8H
0034  umov W0, V30.H[0]
0038  ret
