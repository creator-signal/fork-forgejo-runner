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
    v13_backport = POLICY.get("sourceBackports", {}).get("v13.0.0", {})
    require(
        v13_backport.get("upstreamCommit")
        == "d4db4179a9ba6a0d07e63b8cf382d90fccb2ff21",
        "runner-release-policy.json: v13.0.0 must pin the upstream PTY fix commit",
        errors,
    )
    require(
        v13_backport.get("upstreamPullRequest")
        == "https://code.forgejo.org/forgejo/runner/pulls/1692",
        "runner-release-policy.json: v13.0.0 backport provenance is missing",
        errors,
    )
    require(
        len(v13_backport.get("changedPaths", [])) == 7,
        "runner-release-policy.json: v13.0.0 backport path inventory drifted",
        errors,
    )
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

    qualification_path = WORKFLOW_DIR / "runner-qualification.yml"
    release_path = WORKFLOW_DIR / "runner-release.yml"
    sync_path = WORKFLOW_DIR / "upstream-sync.yml"
    validation_path = WORKFLOW_DIR / "automation-validation.yml"
    verification_path = WORKFLOW_DIR / "runner-release-verification.yml"
    for path in (qualification_path, release_path, sync_path, validation_path, verification_path):
        require(path.is_file(), f"missing workflow: {path.name}", errors)
    if qualification_path.is_file():
        qualification = qualification_path.read_text(encoding="utf-8")
        for artifact in POLICY["artifacts"]:
            require(
                artifact["nativeRunner"] in qualification,
                f"runner-qualification.yml: missing native runner {artifact['nativeRunner']}",
                errors,
            )
            expected = f"{artifact['os']}-{artifact['arch']}"
            require(expected in qualification, f"runner-qualification.yml: missing artifact {expected}", errors)
        windows_start = qualification.find("build-windows-amd64:")
        finalize_start = qualification.find("finalize:")
        linux_start = qualification.find("build-linux-amd64:")
        linux_arm_start = qualification.find("build-linux-arm64:")
        linux_job = qualification[linux_start:linux_arm_start]
        windows_job = qualification[windows_start:finalize_start]
        require(qualification.count("apply_source_backport.py") == 3, "runner-qualification.yml: the governed backport must be applied to all three native builds", errors)
        require("--expected-patch-sha256" in qualification and "--expected-tree-sha" in qualification, "runner-qualification.yml: backport patch/tree identity is not fail-closed", errors)
        require("--backport-commit" in qualification and "--backport-patch-sha256" in qualification and "--patched-source-tree-sha" in qualification, "runner-qualification.yml: SBOM/release backport provenance is incomplete", errors)
        require(linux_start >= 0 and linux_arm_start > linux_start, "runner-qualification.yml: Linux amd64/arm64 jobs not found", errors)
        require("lxc_prepare_environment" in linux_job and "lxc_install_lxc_inside 10.39.28 fdb1" in linux_job, "runner-qualification.yml: Linux LXC preparation is incomplete", errors)
        require("debian-archive-keyring" in linux_job and "/usr/share/keyrings/debian-archive-bookworm-stable.gpg" in linux_job and "/etc/apt/trusted.gpg.d/debian-archive-bookworm-stable.gpg" in linux_job, "runner-qualification.yml: Linux LXC preparation does not seed the current Bookworm archive trust root", errors)
        require("ip -o -4 addr show dev lxcbr0 scope global" in linux_job and "ipaddress.ip_interface" in linux_job and "interface.network.prefixlen == 24" in linux_job, "runner-qualification.yml: Linux LXC bridge subnet is not derived and narrowly validated", errors)
        require("${#lxc_addresses[@]}" in linux_job and "Invalid or unsafe lxcbr0 IPv4 CIDR" in linux_job, "runner-qualification.yml: Linux LXC bridge subnet discovery does not fail closed", errors)
        require("ip -4 route show default" in linux_job and "${#egress_interfaces[@]}" in linux_job and 'ip link show dev "$egress_interface"' in linux_job, "runner-qualification.yml: Linux LXC egress interface is not derived and validated", errors)
        require("sysctl -n net.ipv4.ip_forward" in linux_job and "sysctl -w net.ipv4.ip_forward=1" in linux_job, "runner-qualification.yml: Linux LXC IPv4 forwarding is not enabled conditionally", errors)
        firewall_rules = (
            ('iptables', 'FORWARD -i "$egress_interface" -o lxcbr0 -d "$lxc_subnet" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT'),
            ('iptables', 'FORWARD -i lxcbr0 -o "$egress_interface" -s "$lxc_subnet" -j ACCEPT'),
            ('iptables -t nat', 'POSTROUTING -s "$lxc_subnet" -o "$egress_interface" -j MASQUERADE'),
        )
        for command, rule in firewall_rules:
            require(f"{command} -C {rule}" in linux_job and f"{command} -A {rule}" in linux_job, f"runner-qualification.yml: Linux LXC firewall rule is not scoped and idempotent: {rule}", errors)
        require(not re.search(r"(?m)\\biptables\\b[^\\n]*(?:\\s-(?:F|P)\\b|--flush\\b|--policy\\b)", linux_job), "runner-qualification.yml: Linux LXC firewall setup flushes a table or changes a global policy", errors)
        require("git config --global gc.auto 0" in linux_job and "git config --global maintenance.auto false" in linux_job, "runner-qualification.yml: Linux tests do not suppress background Git maintenance during temporary-repository assertions", errors)
        require("::stop-commands::" in linux_job and "trap 'echo \"::$command_token::\"' EXIT" in linux_job, "runner-qualification.yml: Linux test output can be interpreted as GitHub workflow commands", errors)
        require("go test -count=1 -race -v -timeout 45m ./..." in linux_job and "-json" not in linux_job, "runner-qualification.yml: Linux tests must be complete, uncached, race-enabled, and avoid the upstream stdout-capture JSON failure", errors)
        require("-p 1" not in linux_job and "Large_Fast_Logs" not in linux_job, "runner-qualification.yml: high-throughput LXC tests must not be serialized or excluded", errors)
        require("out/linux-amd64-tests.log" in linux_job and "linux-amd64-tests.jsonl" not in linux_job, "runner-qualification.yml: Linux plain test transcript contract is missing", errors)
        require(windows_start >= 0 and finalize_start > windows_start, "runner-qualification.yml: Windows/finalize jobs not found", errors)
        require(not re.search(r"(?i)\b(?:wsl|podman|docker)\b", windows_job), "runner-qualification.yml: Windows job introduces a WSL/container prerequisite", errors)
        require("./internal/..." in windows_job, "runner-qualification.yml: native Windows runner tests are missing", errors)
        require("./act/..." not in windows_job, "runner-qualification.yml: container-capable act tests must remain on Linux", errors)
        require("-count=1 -short" in windows_job and "-json" not in windows_job, "runner-qualification.yml: Windows tests must be uncached and avoid the upstream stdout-capture JSON failure", errors)
        require("contents: write" not in qualification and "id-token: write" not in qualification and "attestations: write" not in qualification, "runner-qualification.yml: read-only qualification requests publication permissions", errors)
    if release_path.is_file():
        release = release_path.read_text(encoding="utf-8")
        require("contents: write" in release and "id-token: write" in release and "attestations: write" in release, "runner-release.yml: publication permissions are incomplete", errors)
        require("uses: ./.github/workflows/runner-qualification.yml" in release, "runner-release.yml: read-only qualification dependency is missing", errors)
        require("needs: qualify" in release, "runner-release.yml: publisher is not gated on qualification", errors)
        require("GH_REPO: ${{ github.repository }}" in release, "runner-release.yml: GitHub CLI repository binding is missing", errors)
        require("--clobber" not in release, "runner-release.yml: release asset replacement is prohibited", errors)
        require("gh attestation verify" in release, "runner-release.yml: idempotent attestation verification is missing", errors)
        require("--backport-pull-request" in release and release.count("--backport-patch-sha256") >= 3, "runner-release.yml: published backport provenance is incomplete", errors)
        require("Creator Signal-tested; upstream-unsupported" in combined, "runner-release.yml: Windows support boundary is missing", errors)
    if verification_path.is_file():
        verification = verification_path.read_text(encoding="utf-8")
        for native_runner in ("ubuntu-24.04", "ubuntu-24.04-arm", "windows-2025"):
            require(native_runner in verification, f"runner-release-verification.yml: missing {native_runner}", errors)
        require("gh release download" in verification, "runner-release-verification.yml: independent download is missing", errors)
        require("https://slsa.dev/provenance/v1" in verification, "runner-release-verification.yml: provenance verification is missing", errors)
        require("https://spdx.dev/Document/v2.3" in verification, "runner-release-verification.yml: SBOM attestation verification is missing", errors)
        require(verification.count("--backport-patch-sha256") == 3 and verification.count("--patched-source-tree-sha") == 3, "runner-release-verification.yml: independent backport provenance verification is incomplete", errors)
    if validation_path.is_file():
        validation = validation_path.read_text(encoding="utf-8")
        require("inputs: .github/workflows" in validation, "automation-validation.yml: zizmor must remain scoped to governed workflows", errors)
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
    require("source_backport_plan" in control and '["git", "apply", "--cached", "--whitespace=error-all", "-"]' in control and "patchedSourceTreeSha" in control, "release_control.py: governed source backport controls are incomplete", errors)
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
