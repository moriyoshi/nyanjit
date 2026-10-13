0000  ldr Q0, [X1]
0004  ldr Q1, [X2]
0008  add V0.4S, V0.4S, V1.4S
000c  str Q0, [X0]
0010  movz X0, #0x0
0014  ret
