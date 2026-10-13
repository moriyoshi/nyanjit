load("backend_parity.star", "run_arch")
def main():
    run_arch("riscv64", [("baseline", "rv64,zba=false,zbb=false,zbs=false"), ("max", "rv64,zba=true,zbb=true,zbs=true")])
