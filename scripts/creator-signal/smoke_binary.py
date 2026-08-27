#!/usr/bin/env python3
"""Run cross-platform version and generated-configuration smoke checks."""

from __future__ import annotations

import argparse
from pathlib import Path
import subprocess


def invoke(binary: Path, *args: str) -> str:
    result = subprocess.run(
        [str(binary), *args],
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        timeout=30,
    )
    if result.returncode:
        raise SystemExit(
            f"{binary.name} {' '.join(args)} failed ({result.returncode}):\n{result.stdout}"
        )
    return result.stdout


def require_config_load(binary: Path, config: Path) -> None:
    result = subprocess.run(
        [str(binary), "--config", str(config), "daemon"],
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        timeout=30,
    )
    expected = "runner: 0 server connections configured, terminating"
    if result.returncode == 0 or expected not in result.stdout:
        raise SystemExit(
            "generated configuration load smoke did not reach the expected safe "
            f"no-connections boundary (exit={result.returncode}):\n{result.stdout}"
        )


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--config", type=Path, required=True)
    args = parser.parse_args()
    binary = args.binary.resolve()
    actual_version = invoke(binary, "--version").strip()
    expected_version = f"forgejo-runner version {args.tag}"
    if actual_version != expected_version:
        raise SystemExit(
            f"version mismatch: expected {expected_version!r}, got {actual_version!r}"
        )
    config = invoke(binary, "generate-config")
    required_sections = ("log:", "runner:", "container:")
    missing = [section for section in required_sections if section not in config]
    if missing:
        raise SystemExit(f"generated configuration is missing sections: {', '.join(missing)}")
    args.config.write_text(config, encoding="utf-8")
    require_config_load(binary, args.config.resolve())
    invoke(binary, "daemon", "--help")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
