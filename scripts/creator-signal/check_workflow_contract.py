#!/usr/bin/env python3
"""Fail closed when Creator Signal workflow security/release contracts drift."""

from __future__ import annotations

import json
from pathlib import Path
import re
import sys


ROOT = Path(__file__).resolve().parents[2]
WORKFLOW_DIR = ROOT / ".github" / "workflows"
POLICY = json.loads(
    (ROOT / "creator-signal" / "runner-release-policy.json").read_text(encoding="utf-8")
)
SHA_PIN = re.compile(r"^[^\s@]+@[0-9a-f]{40}(?:\s+#.*)?$")


def require(condition: bool, message: str, errors: list[str]) -> None:
    if not condition:
        errors.append(message)


def main() -> int:
    errors: list[str] = []
    workflows = sorted(WORKFLOW_DIR.glob("*.yml"))
    require(bool(workflows), "no GitHub workflows found", errors)
    combined = "\n".join(path.read_text(encoding="utf-8") for path in workflows)
    for path in workflows:
        text = path.read_text(encoding="utf-8")
        require("permissions:\n  contents: read" in text, f"{path.name}: top-level permissions are not read-only", errors)
        require("timeout-minutes:" in text, f"{path.name}: missing bounded job timeout", errors)
        require("concurrency:" in text, f"{path.name}: missing concurrency control", errors)
        require("pull_request_target:" not in text, f"{path.name}: pull_request_target is prohibited", errors)
        for line_number, line in enumerate(text.splitlines(), 1):
            match = re.search(r"\buses:\s*(\S.*)$", line)
            if not match:
                continue
            reference = match.group(1).strip()
            if reference.startswith("./"):
                continue
            require(
                bool(SHA_PIN.fullmatch(reference)),
                f"{path.name}:{line_number}: action is not pinned to a full commit SHA: {reference}",
                errors,
            )

    release_path = WORKFLOW_DIR / "runner-release.yml"
    sync_path = WORKFLOW_DIR / "upstream-sync.yml"
    validation_path = WORKFLOW_DIR / "automation-validation.yml"
    verification_path = WORKFLOW_DIR / "runner-release-verification.yml"
    for path in (release_path, sync_path, validation_path, verification_path):
        require(path.is_file(), f"missing workflow: {path.name}", errors)
    if release_path.is_file():
        release = release_path.read_text(encoding="utf-8")
        for artifact in POLICY["artifacts"]:
            require(
                artifact["nativeRunner"] in release,
                f"runner-release.yml: missing native runner {artifact['nativeRunner']}",
                errors,
            )
            expected = f"{artifact['os']}-{artifact['arch']}"
            require(expected in release, f"runner-release.yml: missing artifact {expected}", errors)
        windows_start = release.find("build-windows-amd64:")
        finalize_start = release.find("finalize:")
        windows_job = release[windows_start:finalize_start]
        require(windows_start >= 0 and finalize_start > windows_start, "runner-release.yml: Windows/finalize jobs not found", errors)
        require(not re.search(r"(?i)\b(?:wsl|podman|docker)\b", windows_job), "runner-release.yml: Windows job introduces a WSL/container prerequisite", errors)
        require("./internal/..." in windows_job, "runner-release.yml: native Windows runner tests are missing", errors)
        require("./act/..." not in windows_job, "runner-release.yml: container-capable act tests must remain on Linux", errors)
        require("go test -json -race -timeout 45m ./..." in release, "runner-release.yml: complete Linux test suite is missing", errors)
        require("contents: write" in release and "id-token: write" in release and "attestations: write" in release, "runner-release.yml: publication permissions are incomplete", errors)
        require("needs: [source, build-linux-amd64, build-linux-arm64, build-windows-amd64]" in release, "runner-release.yml: finalizer is not gated on every source/build job", errors)
        require("--clobber" not in release, "runner-release.yml: release asset replacement is prohibited", errors)
        require("gh attestation verify" in release, "runner-release.yml: idempotent attestation verification is missing", errors)
        require("Creator Signal-tested; upstream-unsupported" in combined, "runner-release.yml: Windows support boundary is missing", errors)
    if verification_path.is_file():
        verification = verification_path.read_text(encoding="utf-8")
        for native_runner in ("ubuntu-24.04", "ubuntu-24.04-arm", "windows-2025"):
            require(native_runner in verification, f"runner-release-verification.yml: missing {native_runner}", errors)
        require("gh release download" in verification, "runner-release-verification.yml: independent download is missing", errors)
        require("https://slsa.dev/provenance/v1" in verification, "runner-release-verification.yml: provenance verification is missing", errors)
        require("https://spdx.dev/Document/v2.3" in verification, "runner-release-verification.yml: SBOM attestation verification is missing", errors)
    if sync_path.is_file():
        sync = sync_path.read_text(encoding="utf-8")
        require("37 4 * * *" in sync, "upstream-sync.yml: schedule must remain non-top-of-hour", errors)
        require("--mode dry-run" in sync, "upstream-sync.yml: manual dry-run is missing", errors)
        require("--mode apply" in sync, "upstream-sync.yml: apply path is missing", errors)
        require("force" not in sync.lower(), "upstream-sync.yml: force operations are prohibited", errors)
        require("delete" not in sync.lower(), "upstream-sync.yml: delete operations are prohibited", errors)
    control = (ROOT / "scripts" / "creator-signal" / "release_control.py").read_text(encoding="utf-8")
    require('"push", "--atomic"' in control, "release_control.py: synchronization push is not atomic", errors)
    require("reject-mismatch" in control, "release_control.py: immutable tag mismatch shield is missing", errors)
    require("immutable rerun mismatch" in control, "release_control.py: rerun byte verification is missing", errors)
    require("ghcr.io" not in combined.lower(), "Runner workflows must not publish containers", errors)
    require(not re.search(r"(?i)(dockerhub|docker hub|amazon s3|\bs3\b)", combined), "Runner workflows contain an unauthorized publication destination", errors)

    if errors:
        print("Workflow contract violations:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1
    print(f"Validated {len(workflows)} Creator Signal workflows and the governed release matrix.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
