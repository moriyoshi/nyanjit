/* Freestanding LP64D probe; regenerated with the pinned L2 musl toolchain.
 * See README.md. The opaque asm prevents folding the address-alignment check. */
unsigned long aligned_local(unsigned long seed) {
    volatile unsigned __int128 local __attribute__((aligned(16))) =
        ((unsigned __int128)seed << 64) | (seed + 1);
    unsigned long address = (unsigned long)&local;
    __asm__ volatile("" : "+r"(address) : : "memory");
    if (address & 15) return address & 15;
    return ((unsigned long)local != seed + 1 ||
            (unsigned long)(local >> 64) != seed) ? 0x1000 : 0;
}
