#!/usr/bin/env python3
"""Build verified E2E VM disks inside Docker build stages."""

import argparse
import bz2
import gzip
import http.server
import json
import lzma
from pathlib import Path
import shutil
import socket
import struct
import subprocess
import sys
import threading
import time

import pexpect


ROOT = Path("/work")
OUT = Path("/vm")


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def selected(arch, selection):
    return arch in {part.strip() for part in selection.split(",")}


def patch_x86_iso(media, arch):
    source = media / "cd79.iso"
    image = bytearray(source.read_bytes())
    filename = b"BOOT.CONF"
    match = image.find(filename)
    if match < 0 or image.find(filename, match + 1, 65536) >= 0:
        raise RuntimeError("cannot identify ISO boot.conf record")
    record = match - 33
    old = f"set image /7.9/{arch}/bsd.rd\n".encode()
    size = struct.unpack_from("<I", image, record + 10)[0]
    offset = struct.unpack_from("<I", image, record + 2)[0] * 2048
    if size != len(old) or image[offset : offset + size] != old:
        raise RuntimeError("ISO boot.conf differs from verified release")
    new = b"set tty com0\n" + old
    if len(new) > 2048:
        raise RuntimeError("serial boot.conf exceeds its ISO sector")
    image[offset : offset + len(new)] = new
    struct.pack_into("<I", image, record + 10, len(new))
    struct.pack_into(">I", image, record + 14, len(new))
    destination = ROOT / "boot-serial.iso"
    destination.write_bytes(image)
    return destination


def keypair():
    OUT.mkdir(parents=True, exist_ok=True)
    key = OUT / "id_ed25519"
    run("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(key))
    return (OUT / "id_ed25519.pub").read_text().strip()


def web_server(arch, public_key):
    web = ROOT / "web"
    sets = web / "7.9" / arch
    sets.parent.mkdir(parents=True, exist_ok=True)
    sets.symlink_to(ROOT / "media" / arch, target_is_directory=True)
    partition_answer = ("Use (W)hole disk MBR, whole disk (G)PT or (E)dit = whole\n"
                        if arch in ("amd64", "i386") else "")
    (web / "install.conf").write_text(
        f"""System hostname = nyanjit-openbsd
Which network interface do you wish to configure = vio0
IPv4 address for vio0 = dhcp
IPv6 address for vio0 = none
Password for root = *************
Public ssh key for root account = {public_key}
Start sshd(8) by default = yes
Allow root ssh login = prohibit-password
Do you expect to run the X Window System = no
Change the default console to com0 = yes
Which speed should com0 use = 115200
Which disk is the root disk = sd0
{partition_answer}
Use (A)uto layout = a
Location of sets = http
HTTP Server = 10.0.2.2:8000
Server directory = /7.9/{arch}
Set name(s) = -x* -game* -comp*
What timezone are you in = UTC
"""
    )
    handler = lambda *args, **kwargs: http.server.SimpleHTTPRequestHandler(
        *args, directory=str(web), **kwargs
    )
    server = http.server.ThreadingHTTPServer(("0.0.0.0", 8000), handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


def x86_qemu(arch, disk, installer=None, ssh_port=None):
    machine = "q35" if arch == "amd64" else "pc"
    executable = "qemu-system-x86_64" if arch == "amd64" else "qemu-system-i386"
    args = [executable, "-M", f"{machine},accel=tcg", "-cpu", "max", "-m", "2048",
            "-smp", "2", "-display", "none", "-vga", "std", "-monitor", "none",
            "-serial", "stdio", "-drive", f"file={disk},if=virtio,format=qcow2"]
    if installer:
        args += ["-cdrom", str(installer), "-boot", "d"]
    else:
        args += ["-boot", "c"]
    network = "user,id=net0"
    if ssh_port:
        network += f",hostfwd=tcp:127.0.0.1:{ssh_port}-:22"
    args += ["-netdev", network, "-device", "virtio-net-pci,netdev=net0"]
    return args


def arm64_openbsd_qemu(disk, installer=None, ssh_port=None):
    args = ["qemu-system-aarch64", "-M", "virt,accel=tcg", "-cpu", "cortex-a72",
            "-m", "2048", "-smp", "2", "-bios", "/usr/share/qemu-efi-aarch64/QEMU_EFI.fd",
            "-display", "none", "-monitor", "none", "-serial", "stdio",
            "-drive", f"file={disk},if=virtio,format=qcow2"]
    if installer:
        args += ["-drive", f"file={installer},if=virtio,format=raw,readonly=on"]
    network = "user,id=net0"
    if ssh_port:
        network += f",hostfwd=tcp:127.0.0.1:{ssh_port}-:22"
    args += ["-netdev", network, "-device", "virtio-net-pci,netdev=net0"]
    return args


def riscv64_openbsd_qemu(disk, installer=None, ssh_port=None):
    args = ["qemu-system-riscv64", "-M", "virt,accel=tcg", "-cpu", "rv64",
            "-m", "2048", "-smp", "2",
            "-bios", "/usr/share/qemu/opensbi-riscv64-generic-fw_dynamic.bin",
            "-kernel", "/usr/lib/u-boot/qemu-riscv64_smode/u-boot.bin",
            "-display", "none", "-monitor", "none", "-serial", "stdio",
            "-drive", f"file={disk},if=virtio,format=qcow2"]
    if installer:
        args += ["-drive", f"file={installer},if=virtio,format=raw,readonly=on"]
    network = "user,id=net0"
    if ssh_port:
        network += f",hostfwd=tcp:127.0.0.1:{ssh_port}-:22"
    args += ["-netdev", network, "-device", "virtio-net-pci,netdev=net0"]
    return args


def drive_openbsd_installer(args):
    log = OUT / "install.serial.log"
    with log.open("w", buffering=1) as transcript:
        child = pexpect.spawn(args[0], args[1:], encoding="utf-8",
                              codec_errors="replace", timeout=300)
        child.logfile_read = transcript
        try:
            child.expect(r"\(I\)nstall, \(U\)pgrade, \(A\)utoinstall or \(S\)hell\?", timeout=600)
            child.sendline("a")
            child.expect(r"Response file location\?[^\n]*", timeout=300)
            child.sendline("http://10.0.2.2:8000/install.conf")
            child.expect([r"(?i)installation complete", r"(?i)congratulations",
                          r"(?i)exit to.*(?:halt|reboot)"], timeout=3600)
            # autoinstall reboots without another prompt. The disk is synced
            # before that reboot; stop QEMU so it cannot start the installer again.
            child.expect(r"syncing disks\.\.\. done", timeout=180)
        finally:
            if child.isalive():
                child.terminate(force=True)


def install_openbsd_x86(arch):
    media = ROOT / "media" / arch
    public_key = keypair()
    installer = patch_x86_iso(media, arch)
    disk = ROOT / "installed.qcow2"
    run("qemu-img", "create", "-f", "qcow2", str(disk), "12G")
    server = web_server(arch, public_key)
    try:
        drive_openbsd_installer(x86_qemu(arch, disk, installer))
    finally:
        server.shutdown()
    verify_openbsd_x86(arch, disk)
    run("qemu-img", "convert", "-c", "-O", "qcow2", str(disk), str(OUT / "disk.qcow2"))
    (OUT / "boot.json").write_text(json.dumps({
        "os": "openbsd", "version": "7.9", "arch": arch,
        "machine": "q35" if arch == "amd64" else "pc", "cpu": "max",
        "firmware": "bios", "disk_bus": "virtio", "nic": "virtio-net-pci",
        "ssh_user": "root", "expected_uname_m": arch,
    }, indent=2) + "\n")


def install_openbsd_arm64():
    public_key = keypair()
    disk = ROOT / "installed.qcow2"
    run("qemu-img", "create", "-f", "qcow2", str(disk), "12G")
    server = web_server("arm64", public_key)
    try:
        drive_openbsd_installer(arm64_openbsd_qemu(
            disk, ROOT / "media" / "arm64" / "miniroot79.img"))
    finally:
        server.shutdown()
    actual = wait_for_ssh(arm64_openbsd_qemu(disk, ssh_port=22003), ("arm64",), 1200)
    run("qemu-img", "convert", "-c", "-O", "qcow2", str(disk), str(OUT / "disk.qcow2"))
    (OUT / "boot.json").write_text(json.dumps({
        "os": "openbsd", "version": "7.9", "arch": "arm64", "machine": "virt",
        "cpu": "cortex-a72", "firmware": "aarch64-uefi", "disk_bus": "virtio",
        "nic": "virtio-net-pci", "ssh_user": "root", "expected_uname_m": actual,
    }, indent=2) + "\n")


def install_openbsd_riscv64():
    public_key = keypair()
    disk = ROOT / "installed.qcow2"
    run("qemu-img", "create", "-f", "qcow2", str(disk), "12G")
    server = web_server("riscv64", public_key)
    try:
        drive_openbsd_installer(riscv64_openbsd_qemu(
            disk, ROOT / "media" / "riscv64" / "miniroot79.img"))
    finally:
        server.shutdown()
    actual = wait_for_ssh(riscv64_openbsd_qemu(disk, ssh_port=22003), ("riscv64",), 1200)
    run("qemu-img", "convert", "-c", "-O", "qcow2", str(disk), str(OUT / "disk.qcow2"))
    (OUT / "boot.json").write_text(json.dumps({
        "os": "openbsd", "version": "7.9", "arch": "riscv64", "machine": "virt",
        "cpu": "rv64", "firmware": "opensbi-uboot", "disk_bus": "virtio",
        "nic": "virtio-net-pci", "ssh_user": "root", "expected_uname_m": actual,
    }, indent=2) + "\n")


def armv7_openbsd_qemu(disk, installer=None, ssh_port=None):
    args = ["qemu-system-arm", "-M", "virt,highmem=off,accel=tcg", "-cpu", "cortex-a15",
            "-m", "2048", "-smp", "2", "-bios", "/usr/share/AAVMF/AAVMF32_CODE.fd",
            "-display", "none", "-monitor", "none", "-serial", "stdio",
            "-drive", f"file={disk},if=virtio,format=qcow2"]
    if installer:
        args += ["-drive", f"file={installer},if=virtio,format=raw,readonly=on"]
    network = "user,id=net0"
    if ssh_port:
        network += f",hostfwd=tcp:127.0.0.1:{ssh_port}-:22"
    args += ["-netdev", network, "-device", "virtio-net-pci,netdev=net0"]
    return args


def armv7_installer_disk():
    media = ROOT / "media" / "armv7"
    esp = ROOT / "openbsd-armv7-installer.img"
    with esp.open("wb") as file:
        file.truncate(64 * 1024 * 1024)
    run("mkfs.vfat", str(esp), stdout=subprocess.DEVNULL)
    run("mmd", "-i", str(esp), "::/EFI", "::/EFI/BOOT")
    run("mcopy", "-i", str(esp), str(media / "BOOTARM.EFI"), "::/EFI/BOOT/BOOTARM.EFI")
    run("mcopy", "-i", str(esp), str(media / "bsd.rd"), "::/bsd.rd")
    return esp


def install_openbsd_armv7():
    public_key = keypair()
    disk = ROOT / "installed.qcow2"
    run("qemu-img", "create", "-f", "qcow2", str(disk), "8G")
    installer = armv7_installer_disk()
    server = web_server("armv7", public_key)
    try:
        drive_openbsd_installer(armv7_openbsd_qemu(disk, installer))
    finally:
        server.shutdown()
    actual = wait_for_ssh(armv7_openbsd_qemu(disk, ssh_port=22003), ("armv7", "arm"), 1200)
    run("qemu-img", "convert", "-c", "-O", "qcow2", str(disk), str(OUT / "disk.qcow2"))
    (OUT / "boot.json").write_text(json.dumps({
        "os": "openbsd", "version": "7.9", "arch": "arm", "machine": "virt,highmem=off",
        "cpu": "cortex-a15", "firmware": "arm-uefi", "disk_bus": "virtio",
        "nic": "virtio-net-pci", "ssh_user": "root", "expected_uname_m": actual,
    }, indent=2) + "\n")


def probe_openbsd_powerpc64():
    media = ROOT / "media" / "powerpc64"
    args = ["qemu-system-ppc64", "-M", "powernv9,accel=tcg", "-cpu", "power9",
            "-m", "2048", "-smp", "2", "-display", "none", "-monitor", "none",
            "-serial", "stdio", "-kernel", str(media / "bsd.rd")]
    with (OUT / "install.serial.log").open("w", buffering=1) as transcript:
        child = pexpect.spawn(args[0], args[1:], encoding="utf-8",
                              codec_errors="replace", timeout=240)
        child.logfile_read = transcript
        try:
            try:
                child.expect(r"\(I\)nstall, \(U\)pgrade, \(A\)utoinstall or \(S\)hell\?", timeout=90)
            except pexpect.TIMEOUT as exc:
                raise RuntimeError("powernv9 bsd.rd did not reach the installer; serial stopped after 'Hello, World!'") from exc
            raise RuntimeError("powerpc64 installer reached but disk boot is not established")
        finally:
            if child.isalive():
                child.terminate(force=True)


def verify_openbsd_x86(arch, disk):
    args = x86_qemu(arch, disk, ssh_port=22003)
    with (OUT / "check.serial.log").open("w") as serial:
        proc = subprocess.Popen(args, stdin=subprocess.DEVNULL, stdout=serial,
                                stderr=subprocess.STDOUT)
        try:
            ssh = ["ssh", "-i", str(OUT / "id_ed25519"), "-p", "22003", "-o", "BatchMode=yes",
                   "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
                   "-o", "ConnectTimeout=5", "root@127.0.0.1"]
            deadline = time.monotonic() + 600
            while time.monotonic() < deadline:
                result = subprocess.run(ssh + ["uname -m"], capture_output=True, text=True)
                if result.returncode == 0:
                    actual = result.stdout.strip()
                    if actual != arch:
                        raise RuntimeError(f"OpenBSD {arch} uname -m returned {actual!r}")
                    subprocess.run(ssh + ["shutdown -p now"], capture_output=True, timeout=15)
                    return
                time.sleep(5)
            raise RuntimeError(f"OpenBSD {arch} SSH check timed out")
        finally:
            try:
                proc.wait(timeout=90)
            except subprocess.TimeoutExpired:
                proc.terminate()
                proc.wait(timeout=15)


def wait_for_ssh(args, expected, timeout=900, compat=None):
    """Boot an image and require an authenticated, correct-architecture SSH reply."""
    with (OUT / "check.serial.log").open("w") as serial:
        proc = subprocess.Popen(args, stdin=subprocess.DEVNULL, stdout=serial,
                                stderr=subprocess.STDOUT)
        try:
            ssh = ["ssh", "-i", str(OUT / "id_ed25519"), "-p", "22003",
                   "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no",
                   "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=5",
                   "root@127.0.0.1"]
            deadline = time.monotonic() + timeout
            last_error = ""
            while time.monotonic() < deadline:
                result = subprocess.run(ssh + ["uname -m"], capture_output=True, text=True)
                if result.returncode == 0:
                    actual = result.stdout.strip()
                    if actual not in expected:
                        raise RuntimeError(f"guest uname -m returned {actual!r}, expected {expected}")
                    if compat:
                        host_binary = Path("/usr/local/share/nyanjit/probes") / compat
                        scp = ["scp", "-i", str(OUT / "id_ed25519"), "-P", "22003",
                               "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
                               str(host_binary), "root@127.0.0.1:/tmp/nyanjit-compat-probe"]
                        run(*scp, stdout=subprocess.DEVNULL)
                        result = subprocess.run(ssh + ["chmod +x /tmp/nyanjit-compat-probe && /tmp/nyanjit-compat-probe"],
                                                capture_output=True, text=True, timeout=120)
                        if result.returncode != 0 or result.stdout.strip() != compat.replace("-", " "):
                            raise RuntimeError(f"{compat} guest probe failed: {result.returncode} "
                                               f"{result.stdout.strip()} {result.stderr.strip()}")
                    subprocess.run(ssh + ["shutdown -p now"], capture_output=True, timeout=30)
                    return actual
                last_error = (result.stderr or result.stdout).strip()
                time.sleep(5)
            raise RuntimeError(f"SSH check timed out: {last_error}")
        finally:
            try:
                proc.wait(timeout=90)
            except subprocess.TimeoutExpired:
                proc.terminate()
                proc.wait(timeout=15)


def cloud_seed(osname, public_key):
    seed = ROOT / "seed"
    seed.mkdir(exist_ok=True)
    (seed / "meta-data").write_text("instance-id: nyanjit-image\nlocal-hostname: nyanjit-e2e\n")
    if osname == "freebsd":
        script = f"""#!/bin/sh
set -eu
mkdir -p /root/.ssh
printf '%s\\n' '{public_key}' > /root/.ssh/authorized_keys
chmod 700 /root/.ssh
chmod 600 /root/.ssh/authorized_keys
printf '\\nPermitRootLogin prohibit-password\\n' >> /etc/ssh/sshd_config
sysrc sshd_enable=YES
sysrc ifconfig_DEFAULT=DHCP
printf 'console=\"comconsole\"\\n' >> /boot/loader.conf
"""
    else:
        script = f"""#!/bin/sh
set -eu
mkdir -p /root/.ssh
printf '%s\\n' '{public_key}' > /root/.ssh/authorized_keys
chmod 700 /root/.ssh
chmod 600 /root/.ssh/authorized_keys
printf '\\nPermitRootLogin prohibit-password\\n' >> /etc/ssh/sshd_config
svcadm enable ssh
"""
    (seed / "user-data").write_text(script)
    iso = ROOT / "seed.iso"
    run("xorriso", "-as", "mkisofs", "-quiet", "-V", "cidata", "-J", "-r",
        "-o", str(iso), str(seed / "meta-data"), str(seed / "user-data"))
    return iso


def cloud_qemu(osname, arch, disk, seed=None):
    if arch == "arm64":
        args = ["qemu-system-aarch64", "-M", "virt,accel=tcg", "-cpu", "cortex-a72",
                "-bios", "/usr/share/qemu-efi-aarch64/QEMU_EFI.fd"]
    else:
        args = ["qemu-system-x86_64", "-M", "q35,accel=tcg", "-cpu", "max"]
        if osname == "omnios":
            args += ["-drive", "if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/OVMF_CODE_4M.fd"]
    args += ["-m", "3072", "-smp", "2", "-display", "none", "-monitor", "none",
             "-serial", "stdio", "-drive", f"file={disk},if=virtio,format=qcow2",
             "-netdev", "user,id=net0,hostfwd=tcp:127.0.0.1:22003-:22",
             "-device", "virtio-net-pci,netdev=net0"]
    if seed:
        args += ["-drive", f"file={seed},media=cdrom,readonly=on"]
    return args


def build_cloud(osname, arch, selection):
    OUT.mkdir(parents=True, exist_ok=True)
    if not selected(osname, selection):
        (OUT / "NO-RUNNER").write_text(f"{osname} excluded by VM_OSES\n")
        return
    try:
        public_key = keypair()
        media = ROOT / "media" / f"{osname}-{arch}"
        disk = ROOT / "installed.qcow2"
        if osname == "freebsd":
            source = next(media.glob("*.qcow2.xz"))
            with lzma.open(source, "rb") as input_, disk.open("wb") as output:
                shutil.copyfileobj(input_, output)
            expected = ("amd64", "x86_64") if arch == "amd64" else ("arm64", "aarch64")
            version = "15.1"
        else:
            source = next(media.glob("*.cloud.qcow2"))
            shutil.copyfile(source, disk)
            expected = ("i86pc", "amd64", "x86_64")
            version = "r151058"
        seed = cloud_seed(osname, public_key)
        actual = wait_for_ssh(cloud_qemu(osname, arch, disk, seed), expected, 1800)
        # The second boot proves the configured image works without the seed.
        compat = f"freebsd-{'386' if arch == 'amd64' else 'arm'}" if osname == "freebsd" else None
        again = wait_for_ssh(cloud_qemu(osname, arch, disk), expected, 900, compat)
        if actual != again:
            raise RuntimeError(f"uname -m changed between boots: {actual}, {again}")
        run("qemu-img", "convert", "-c", "-O", "qcow2", str(disk), str(OUT / "disk.qcow2"))
        (OUT / "boot.json").write_text(json.dumps({
            "os": osname, "version": version, "arch": arch,
            "machine": "virt" if arch == "arm64" else "q35",
            "cpu": "cortex-a72" if arch == "arm64" else "max",
            "firmware": "aarch64-uefi" if arch == "arm64" else ("ovmf" if osname == "omnios" else "bios"),
            "disk_bus": "virtio", "nic": "virtio-net-pci",
            "ssh_user": "root", "expected_uname_m": actual,
        }, indent=2) + "\n")
    except Exception:
        for path in (OUT / "check.serial.log",):
            if path.exists():
                print("\n".join(path.read_text(errors="replace").splitlines()[-100:]), file=sys.stderr)
        raise


def hmp_keys(socket_path, command):
    mapping = {" ": "spc", "/": "slash", "-": "minus", "'": "apostrophe",
               "\n": "ret"}
    with socket.socket(socket.AF_UNIX) as monitor:
        monitor.connect(str(socket_path))
        monitor.recv(4096)
        for character in command:
            key = mapping.get(character, character)
            monitor.sendall(f"sendkey {key}\n".encode())
            time.sleep(0.06)
            monitor.recv(4096)


def netbsd_qemu(arch, disk, monitor=None, ssh_port=None):
    if arch == "amd64":
        args = ["qemu-system-x86_64", "-M", "q35,accel=tcg", "-cpu", "max"]
    elif arch == "arm64":
        args = ["qemu-system-aarch64", "-M", "virt,accel=tcg", "-cpu", "cortex-a72",
                "-bios", "/usr/share/qemu-efi-aarch64/QEMU_EFI.fd"]
    else:
        args = ["qemu-system-arm", "-M", "virt,highmem=off,accel=tcg", "-cpu", "cortex-a15",
                "-bios", "/usr/share/AAVMF/AAVMF32_CODE.fd"]
    args += ["-m", "2048", "-smp", "2", "-display", "none", "-serial", "stdio",
             "-drive", f"file={disk},if=virtio,format=raw"]
    if monitor:
        args += ["-monitor", f"unix:{monitor},server,nowait"]
    else:
        args += ["-monitor", "none"]
    network = "user,id=net0"
    if ssh_port:
        network += f",hostfwd=tcp:127.0.0.1:{ssh_port}-:22"
    args += ["-netdev", network, "-device", "virtio-net-pci,netdev=net0"]
    return args


def serial_command(child, command, prompt=r"[#] ", timeout=120):
    print(f"guest> {command}", flush=True)
    if len(command) > 80:
        # DragonFly's emulated UART can drop bytes from a full-line write.
        # A lost newline leaves the shell waiting forever for the rest.
        for character in command + "\n":
            child.send(character)
            time.sleep(0.02)
    else:
        child.sendline(command)
    child.expect(prompt, timeout=timeout)
    print(child.before[-500:], flush=True)
    return child.before


def build_netbsd(arch, selection):
    OUT.mkdir(parents=True, exist_ok=True)
    if not selected("netbsd", selection):
        (OUT / "NO-RUNNER").write_text("netbsd excluded by VM_OSES\n")
        return
    public_key = keypair()
    source = next((ROOT / "media" / f"netbsd-{arch}").glob("*.img.gz"))
    disk = ROOT / "installed.img"
    with gzip.open(source, "rb") as input_, disk.open("wb") as output:
        shutil.copyfileobj(input_, output)
    run("qemu-img", "resize", "-f", "raw", str(disk), "8G")
    monitor = ROOT / "qemu-monitor.sock"
    args = netbsd_qemu(arch, disk, monitor)
    with (OUT / "install.serial.log").open("w", buffering=1) as transcript:
        child = pexpect.spawn(args[0], args[1:], encoding="utf-8",
                              codec_errors="replace", timeout=600)
        child.logfile_read = transcript
        try:
            if arch == "amd64":
                # The live image starts on VGA. A brief local-console bootstrap
                # enables tty00; all actual guest configuration then uses serial.
                time.sleep(100)
                hmp_keys(monitor, "root\n")
                time.sleep(5)
                hmp_keys(monitor, "sed -i -e 's/off secure/on secure/' /etc/ttys\n")
                hmp_keys(monitor, "kill -1 1\n")
            child.expect(r"login: ", timeout=600)
            child.sendline("root")
            child.expect(r"[#] ", timeout=120)
            commands = [
                "mkdir -p /root/.ssh",
                f"printf '%s\\n' '{public_key}' > /root/.ssh/authorized_keys",
                "chmod 700 /root/.ssh && chmod 600 /root/.ssh/authorized_keys",
                "printf '\\nsshd=YES\\ndhcpcd=YES\\n' >> /etc/rc.conf",
                "sed -i -e '/^PermitRootLogin/d' /etc/ssh/sshd_config",
                "printf '\\nPermitRootLogin prohibit-password\\n' >> /etc/ssh/sshd_config",
            ]
            if arch == "amd64":
                commands.append("printf '\\nconsdev com0\\n' >> /boot.cfg")
            for command in commands:
                serial_command(child, command)
            serial_command(child, "/etc/rc.d/sshd restart")
            child.sendline("shutdown -p now")
            child.expect(pexpect.EOF, timeout=180)
        finally:
            if child.isalive():
                child.terminate(force=True)
    # Check from a new boot, without the installer console. For NetBSD/386
    # and NetBSD/arm, the compatibility probe is run in this check boot.
    expected = {"amd64": ("amd64",), "arm64": ("aarch64", "evbarm"),
                "arm": ("evbarm",)}[arch]
    verify = netbsd_qemu(arch, disk, ssh_port=22003)
    actual = wait_for_ssh(verify, expected, 1200,
                          "netbsd-386" if arch == "amd64" else None)
    run("qemu-img", "convert", "-c", "-f", "raw", "-O", "qcow2",
        str(disk), str(OUT / "disk.qcow2"))
    (OUT / "boot.json").write_text(json.dumps({
        "os": "netbsd", "version": "11.0", "arch": arch,
        "machine": "q35" if arch == "amd64" else "virt",
        "cpu": "max" if arch == "amd64" else ("cortex-a72" if arch == "arm64" else "cortex-a15"),
        "firmware": "bios" if arch == "amd64" else ("aarch64-uefi" if arch == "arm64" else "arm-uefi"),
        "disk_bus": "virtio", "nic": "virtio-net-pci",
        "ssh_user": "root", "expected_uname_m": actual,
    }, indent=2) + "\n")


def dragonfly_qemu(disk, monitor=None, ssh_port=None):
    args = ["qemu-system-x86_64", "-M", "q35,accel=tcg", "-cpu", "max",
            "-m", "2048", "-smp", "2", "-display", "none", "-serial", "stdio",
            "-drive", f"file={disk},if=virtio,format=raw"]
    if monitor:
        args += ["-monitor", f"unix:{monitor},server,nowait"]
    else:
        args += ["-monitor", "none"]
    network = "user,id=net0"
    if ssh_port:
        network += f",hostfwd=tcp:127.0.0.1:{ssh_port}-:22"
    args += ["-netdev", network, "-device", "virtio-net-pci,netdev=net0"]
    return args


def dragonfly_serial_login(child, monitor):
    # The release USB image starts on VGA. Enable its already-configured
    # serial terminal via HMP once, then drive the rest of the boot by serial.
    time.sleep(75)
    for attempt in range(6):
        hmp_keys(monitor, "root\n")
        time.sleep(5)
        hmp_keys(monitor, "sed -i -e '/ttyd0/s/ifconsole//' /etc/ttys\n")
        hmp_keys(monitor, "kill -1 1\n")
        try:
            child.expect(r"login: ", timeout=30)
            break
        except pexpect.TIMEOUT:
            print(f"DragonFly serial bootstrap retry {attempt + 1}/6", flush=True)
    else:
        raise RuntimeError("DragonFly serial login did not appear after VGA bootstrap")
    child.sendline("root")
    child.expect(r"[#] ", timeout=120)


def build_dragonfly(selection):
    OUT.mkdir(parents=True, exist_ok=True)
    if not selected("dragonfly", selection):
        (OUT / "NO-RUNNER").write_text("dragonfly excluded by VM_OSES\n")
        return
    public_key = keypair()
    source = ROOT / "media" / "dragonfly-amd64" / "dfly-x86_64-6.4.2_REL.img.bz2"
    disk = ROOT / "installed.img"
    with bz2.open(source, "rb") as input_, disk.open("wb") as output:
        shutil.copyfileobj(input_, output)
    # The live USB has a writable UFS root. Configure it here, then expand
    # the partition and filesystem from a separate boot so it is unmounted.
    run("qemu-img", "resize", "-f", "raw", str(disk), "8G")
    monitor = ROOT / "qemu-monitor.sock"
    args = dragonfly_qemu(disk, monitor)
    with (OUT / "install.serial.log").open("w", buffering=1) as transcript:
        child = pexpect.spawn(args[0], args[1:], encoding="utf-8",
                              codec_errors="replace", timeout=600)
        child.logfile_read = transcript
        try:
            dragonfly_serial_login(child, monitor)
            commands = [
                "mkdir -p /root/.ssh",
                f"printf '%s\\n' '{public_key}' > /root/.ssh/authorized_keys",
                "chmod 700 /root/.ssh && chmod 600 /root/.ssh/authorized_keys",
                "printf '\\nifconfig_vtnet0=\"DHCP\"\\nsshd_enable=\"YES\"\\n' >> /etc/rc.conf",
                "printf '\\nconsole=\"comconsole\"\\n' >> /boot/loader.conf",
                "sed -i -e '/^PermitRootLogin/d' /etc/ssh/sshd_config",
                "printf '\\nPermitRootLogin prohibit-password\\n' >> /etc/ssh/sshd_config",
                "dhclient vtnet0",
                "/etc/rc.d/sshd start",
            ]
            for command in commands:
                serial_command(child, command, timeout=300)
            child.sendline("shutdown -p now")
            child.expect(pexpect.EOF, timeout=180)
        finally:
            if child.isalive():
                child.terminate(force=True)
    # growfs refuses a mounted UFS root. Boot an untouched copy of the live
    # image as vbd0, attach the configured disk as vbd1, and grow it offline.
    rescue = ROOT / "rescue.img"
    with bz2.open(source, "rb") as input_, rescue.open("wb") as output:
        shutil.copyfileobj(input_, output)
    monitor.unlink(missing_ok=True)
    rescue_args = dragonfly_qemu(rescue, monitor)
    rescue_args += ["-drive", f"file={disk},if=virtio,format=raw"]
    with (OUT / "grow.serial.log").open("w", buffering=1) as transcript:
        child = pexpect.spawn(rescue_args[0], rescue_args[1:], encoding="utf-8",
                              codec_errors="replace", timeout=600)
        child.logfile_read = transcript
        try:
            dragonfly_serial_login(child, monitor)
            child.sendline("fdisk -x vbd1")
            child.expect(r"Should we write new partition table\? \[n\] ", timeout=120)
            child.sendline("y")
            child.expect(r"[#] ", timeout=120)
            output = serial_command(child, "disklabel64 -xx vbd1s2", timeout=120)
            if any(word in output.lower() for word in
                   ("device busy", "no such file", "cannot open", "invalid", "error:")):
                raise RuntimeError(f"offline DragonFly disklabel expansion failed: {output[-500:]}")
            output = serial_command(child, "growfs -y /dev/vbd1s2a", timeout=300)
            if any(word in output.lower() for word in
                   ("device busy", "no such file", "cannot open", "invalid", "error:")):
                raise RuntimeError(f"offline DragonFly growfs failed: {output[-500:]}")
            child.sendline("shutdown -p now")
            child.expect(pexpect.EOF, timeout=180)
        finally:
            if child.isalive():
                child.terminate(force=True)
    actual = wait_for_ssh(dragonfly_qemu(disk, ssh_port=22003), ("x86_64", "amd64"), 1200)
    run("qemu-img", "convert", "-c", "-f", "raw", "-O", "qcow2",
        str(disk), str(OUT / "disk.qcow2"))
    (OUT / "boot.json").write_text(json.dumps({
        "os": "dragonfly", "version": "6.4.2", "arch": "amd64",
        "machine": "q35", "cpu": "max", "firmware": "bios",
        "disk_bus": "virtio", "nic": "virtio-net-pci",
        "ssh_user": "root", "expected_uname_m": actual,
    }, indent=2) + "\n")


def build_openbsd(arch, selection, experimental, os_selection):
    OUT.mkdir(parents=True, exist_ok=True)
    if not selected("openbsd", os_selection):
        (OUT / "NO-RUNNER").write_text("openbsd excluded by VM_OSES\n")
        return
    if not selected(arch, selection):
        (OUT / "NO-RUNNER").write_text("excluded by OPENBSD_ARCHS\n")
        return
    try:
        if arch in ("amd64", "i386"):
            install_openbsd_x86(arch)
        elif arch == "arm64":
            install_openbsd_arm64()
        elif arch == "riscv64":
            install_openbsd_riscv64()
        elif arch == "armv7":
            install_openbsd_armv7()
        elif arch == "powerpc64":
            probe_openbsd_powerpc64()
        else:
            raise RuntimeError(f"{arch} installer boot path is not implemented")
    except Exception as exc:
        if arch == "armv7" and isinstance(exc, pexpect.TIMEOUT):
            exc = RuntimeError("armv7 EDK2 boot did not reach the OpenBSD installer on QEMU virt")
        lines = []
        for path in (OUT / "install.serial.log", OUT / "check.serial.log"):
            if path.exists():
                lines.extend(path.read_text(errors="replace").splitlines())
        if not experimental:
            print("OpenBSD image build failed:", exc, file=sys.stderr)
            print("\n".join(lines[-100:]), file=sys.stderr)
            raise
        (OUT / "NO-RUNNER").write_text(f"{exc}\n" + "\n".join(lines[-100:]) + "\n")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=("build-openbsd", "build-cloud", "build-netbsd", "build-dragonfly"))
    parser.add_argument("arch")
    parser.add_argument("--selection", default="amd64,i386,arm64,riscv64,armv7,powerpc64")
    parser.add_argument("--os-selection", default="openbsd,freebsd,netbsd,dragonfly,omnios")
    parser.add_argument("--experimental", action="store_true")
    args = parser.parse_args()
    try:
        if args.command == "build-openbsd":
            build_openbsd(args.arch, args.selection, args.experimental, args.os_selection)
        elif args.command == "build-cloud":
            osname, arch = args.arch.split("-", 1)
            build_cloud(osname, arch, args.selection)
        elif args.command == "build-netbsd":
            build_netbsd(args.arch, args.selection)
        else:
            build_dragonfly(args.selection)
    except Exception as exc:
        print(f"{args.command} {args.arch}: {exc}", file=sys.stderr)
        for name in ("install.serial.log", "check.serial.log"):
            log = OUT / name
            if log.exists():
                print(f"--- {name} (last 100 lines) ---", file=sys.stderr)
                print("\n".join(log.read_text(errors="replace").splitlines()[-100:]),
                      file=sys.stderr)
        raise


if __name__ == "__main__":
    main()
