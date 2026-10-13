load("backend_parity.star", "run_arch")
def main():
    run_arch("386", [("baseline", "qemu32"), ("max", "max")])
