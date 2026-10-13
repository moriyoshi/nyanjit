# Optional stress gate for the unresolved LoongArch GC observation. Each
# process repeats the complete package, retaining registered modules across
# repeats. A panic or GC-checker failure is a failed check, without retries.
load("targets.star", "targets")

def run_gc_profile(t, binary, cpu, suite):
    result = run(
        t,
        binary,
        args = ["-test.v", "-test.count=3", "-test.timeout=20m"],
        env = ["QEMU_CPU=" + cpu, "GOGC=10", "GODEBUG=gccheckmark=1"],
        timeout = 1260,
    )
    if result.exit_code != 0:
        print(suite + " " + t.go_version + " failure:\n" + result.output)
    check(t, suite, binary, result)

def run_gc_target(t, profiles):
    binaries = build_tests(t, ["github.com/moriyoshi/nyanjit/compile/varfuzz"], timeout = 1200)
    tasks = [go(run_gc_profile, t, binary, cpu, suite) for suite, cpu in profiles for binary in binaries]
    for task in tasks:
        join(task, timeout = 2700)

def main():
    tasks = []
    for t in targets():
        if t.goos != "linux" or t.goarch != "loong64":
            continue
        profiles = []
        for name, cpu in [("baseline", "la464,lsx=off,lasx=off"), ("max", "max")]:
            suite = "loong64-gc-" + name
            if selected(t, suite):
                profiles.append((suite, cpu))
        if profiles:
            tasks.append(go(run_gc_target, t, profiles))
    for task in tasks:
        join(task, timeout = 4200)
