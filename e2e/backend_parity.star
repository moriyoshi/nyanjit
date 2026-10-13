load("targets.star", "targets")

def run_profile(t, binaries, cpu, name):
    for binary in binaries:
        result = run(t, binary, args = ["-test.v", "-test.timeout=10m"], env = ["QEMU_CPU=" + cpu], timeout = 2400)
        check(t, name, binary, result)

def run_target_profiles(t, profiles):
    binaries = build_tests(t, packages(t), timeout = 1200)
    tasks = [go(run_profile, t, binaries, cpu, name) for name, cpu in profiles]
    for task in tasks:
        join(task, timeout = 7200)

def run_arch(architecture, profiles):
    tasks = []
    for t in targets():
        if t.goos == "linux" and t.goarch == architecture:
            chosen = []
            for name, cpu in profiles:
                suite = architecture + "-" + name
                if selected(t, suite):
                    chosen.append((suite, cpu))
            if chosen:
                tasks.append(go(run_target_profiles, t, chosen))
    for task in tasks:
        join(task, timeout = 7200)
