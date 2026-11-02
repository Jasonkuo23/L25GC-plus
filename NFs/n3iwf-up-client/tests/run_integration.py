#!/usr/bin/env python3
"""Isolated real DPDK primary/Go secondary test; never attaches to ONVM.

Requires writable mounted hugepages and permission to initialize DPDK.
All test logs live in a new result directory, with a unique prefix.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fixture", type=Path, required=True)
    parser.add_argument("--go", default="go")
    parser.add_argument("--huge-dir", default="/mnt/huge")
    parser.add_argument("--primary-core", default="16")
    parser.add_argument("--secondary-core", default="17")
    parser.add_argument("--dp-core", default="18")
    parser.add_argument("--crash-core", default="19")
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    output = args.output or Path(tempfile.mkdtemp(prefix="n3dp-shm-integration-"))
    output.mkdir(parents=True, exist_ok=True)
    prefix = f"n3dp_test_{os.getpid()}"
    env = os.environ.copy()
    # RUNTIME_DIRECTORY also works for root EAL; XDG_RUNTIME_DIR is ignored by root.
    env["RUNTIME_DIRECTORY"] = str(output.resolve())
    env["CGO_CFLAGS_ALLOW"] = "-mrtm"
    env["GOCACHE"] = os.environ.get("GOCACHE", str(output.resolve() / "go-cache"))
    env["N3DP_TEST_NAME"] = "N3DP_TEST"
    package = Path(__file__).resolve().parents[1]
    primary_log = output / "primary.log"
    with (output / "manifest.json").open("x") as manifest:
        json.dump({
            "transport": "DPDK memzone/rte_ring",
            "processes": "C primary + C DP secondary + Go CP secondary",
            "fixture_sha256": hashlib.sha256(args.fixture.read_bytes()).hexdigest(),
            "file_prefix": prefix, "primary_core": args.primary_core,
            "dp_core": args.dp_core, "cp_core": args.secondary_core,
            "huge_dir": args.huge_dir, "memory_mb": 32,
            "go_version": subprocess.check_output([args.go, "version"], text=True).strip(),
            "dpdk_version": subprocess.check_output(["pkg-config", "--modversion", "libdpdk"], text=True).strip(),
            "client_sources_sha256": {
                path.name: hashlib.sha256(path.read_bytes()).hexdigest()
                for path in package.iterdir()
                if path.suffix in (".go", ".c", ".h") and not path.name.endswith("_test.go")
            },
            "scope": "isolated CP/UP lifecycle; excludes UE/core/TAP",
        }, manifest, indent=2)
    primary = None
    dataplane = None

    def wait_ready(process, path, marker):
        deadline = time.monotonic() + 15
        while marker not in path.read_text():
            if process.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError(f"fixture startup failed: {path.read_text()}")
            time.sleep(0.05)

    try:
        with primary_log.open("x") as log, (output / "dataplane.log").open("x") as dp_log:
            primary_env = dict(env, N3DP_TEST_PRIMARY_ONLY="1")
            primary = subprocess.Popen([
                str(args.fixture.resolve()), "-l", args.primary_core,
                "--file-prefix", prefix, "--no-pci", "--no-telemetry",
                "--huge-dir", args.huge_dir, "-m", "32",
            ], env=primary_env, stdout=log, stderr=subprocess.STDOUT)
            wait_ready(primary, primary_log, "N3DP primary ready")
            dataplane = subprocess.Popen([
                str(args.fixture.resolve()), "-l", args.dp_core, "--proc-type=secondary",
                "--file-prefix", prefix, "--no-pci", "--no-telemetry",
            ], env=env, stdout=dp_log, stderr=subprocess.STDOUT)
            wait_ready(dataplane, output / "dataplane.log", "N3DP fixture ready")
            env["N3DP_INTEGRATION_EAL_ARGS"] = f"-l {args.secondary_core} --file-prefix {prefix}"
            env["N3DP_INTEGRATION_CRASH_EAL_ARGS"] = f"-l {args.crash_core} --file-prefix {prefix}"
            env["N3DP_INTEGRATION_PID"] = str(dataplane.pid)
            with (output / "go-integration.log").open("x") as testlog:
                subprocess.run([args.go, "test", "-race", "-count=1", "-v", "-run", "^TestDPDKIntegration$"],
                               cwd=package, env=env, stdout=testlog, stderr=subprocess.STDOUT,
                               check=True, timeout=180)
        print(f"PASS: C-secondary/Go-secondary lifecycle: {output}")
    finally:
        for process in (dataplane, primary):
            if process is not None and process.poll() is None:
                # The timeout test may have failed while the fixture was stopped.
                process.send_signal(signal.SIGCONT)
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()


if __name__ == "__main__":
    main()
