load("backend_parity.star", "run_arch")

def main():
    # Verified with tonistiigi QEMU 10.2.3: these are bool CPU properties.
    run_arch("loong64", [("baseline", "la464,lsx=off,lasx=off"), ("max", "max")])
