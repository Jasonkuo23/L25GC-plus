#!/usr/bin/env python3
"""Generate the pinned OAuth cache and ONVM HTTP server source overlays."""

import argparse
import json
from pathlib import Path
import subprocess

MODULE = "github.com/nycu-ucr/openapi@v0.0.0-20250423030004-ca6aa49065b0"
SOURCE = "oauth/get_token_context.go"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--tests", action="store_true", help="include ONVM server regression tests")
    args = parser.parse_args()

    root = Path(__file__).resolve().parents[2]
    module_cache = Path(subprocess.check_output(["go", "env", "GOMODCACHE"], text=True).strip())
    original = module_cache / MODULE / SOURCE
    if not original.is_file():
        subprocess.run(["go", "mod", "download", MODULE], check=True)
    if not original.is_file():
        parser.error(f"pinned OpenAPI module is not downloaded: {original}")

    replacement = root / "patches/openapi/oauth/get_token_context.go"
    overlay = {str(original): str(replacement)}
    net_module = module_cache / "github.com/nycu-ucr/net@v0.0.0-20250421092837-e154d11df6a2"
    server_source = net_module / "http2/onvm_server.go"
    if not server_source.is_file():
        subprocess.run(["go", "mod", "download", "github.com/nycu-ucr/net@v0.0.0-20250421092837-e154d11df6a2"], check=True)
    overlay[str(net_module / "http2/onvm_server.go")] = str(root / "patches/net/http2/onvm_server.go")
    if args.tests:
        # Go 1.21 enumerates existing module test files before applying overlays.
        overlay[str(net_module / "http2/ascii_test.go")] = str(root / "patches/net/http2/onvm_transport_test.go")
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps({"Replace": overlay}, indent=2) + "\n")


if __name__ == "__main__":
    main()
