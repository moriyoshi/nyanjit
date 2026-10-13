load("backend_parity.star", "run_arch")
def main():
    # Native ARM hosts execute both profiles using explicit baseline/profile
    # compiler tests. Foreign hosts use the selected QEMU CPU.
    run_arch("arm64", [("baseline", "cortex-a72"), ("max", "max")])
