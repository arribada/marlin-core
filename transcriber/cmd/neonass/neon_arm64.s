//go:build arm64
// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

#include "textflag.h"

// func neonBurnLoop()
TEXT ·neonBurnLoop(SB), NOSPLIT, $0-0
loop:
    // Load float constants into FP registers F0-F3
    FMOVD $(1.000001), F0
    FMOVD $(1.000002), F1
    FMOVD $(0.500000), F2
    FMOVD $(0.250000), F3

    // Duplicate 32-bit floats across 128-bit NEON vector registers (S4 arrangement)
    VDUP  V0.S[0], V0.S4
    VDUP  V1.S[0], V1.S4
    VDUP  V2.S[0], V2.S4
    VDUP  V3.S[0], V3.S4

    // Unroll 128-bit Vector Fused Multiply-Add (VFMLA) instructions
    // In Go asm, the vector layout suffix is placed after the register name: V0.S4
    VFMLA V1.S4, V2.S4, V0.S4
    VFMLA V2.S4, V3.S4, V1.S4
    VFMLA V3.S4, V0.S4, V2.S4
    VFMLA V0.S4, V1.S4, V3.S4

    VFMLA V1.S4, V2.S4, V0.S4
    VFMLA V2.S4, V3.S4, V1.S4
    VFMLA V3.S4, V0.S4, V2.S4
    VFMLA V0.S4, V1.S4, V3.S4

    JMP loop
