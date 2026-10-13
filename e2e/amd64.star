load("backend_parity.star", "run_arch")
def main():
    run_arch("amd64", [("baseline", "qemu64"), ("max", "max")])
