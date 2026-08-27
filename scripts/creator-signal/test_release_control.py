#!/usr/bin/env python3

from __future__ import annotations

import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock


SCRIPT = Path(__file__).with_name("release_control.py")
SPEC = importlib.util.spec_from_file_location("release_control", SCRIPT)
assert SPEC and SPEC.loader
release_control = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release_control)


def command(*args: str, cwd: Path) -> str:
    result = subprocess.run(
        list(args),
        cwd=cwd,
        check=True,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    return result.stdout.strip()


class ReleaseControlTests(unittest.TestCase):
    def setUp(self) -> None:
        self.policy = release_control.load_policy()

    def test_policy_declares_required_native_matrix(self) -> None:
        matrix = {
            (artifact["os"], artifact["arch"], artifact["nativeRunner"])
            for artifact in self.policy["artifacts"]
        }
        self.assertEqual(
            matrix,
            {
                ("linux", "amd64", "ubuntu-24.04"),
                ("linux", "arm64", "ubuntu-24.04-arm"),
                ("windows", "amd64", "windows-2025"),
            },
        )
        windows = next(item for item in self.policy["artifacts"] if item["os"] == "windows")
        self.assertEqual(windows["upstreamSupport"], "unsupported")

    def test_semver_and_asset_contract(self) -> None:
        self.assertLess(
            release_control.semver_key("v12.13.2"),
            release_control.semver_key("v13.0.0"),
        )
        self.assertIn(
            "forgejo-runner-13.0.0-windows-amd64.exe",
            release_control.expected_asset_names("v13.0.0", self.policy),
        )
        self.assertIn(
            "forgejo-runner-13.0.0-linux-arm64.spdx.json",
            release_control.expected_asset_names("v13.0.0", self.policy),
        )

    def test_prepare_and_verify_release_assets(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            expected = release_control.expected_asset_names("v13.0.0", self.policy)
            for name in expected:
                if name in {"SOURCE-PROVENANCE.json", "SHA256SUMS"}:
                    continue
                if name.endswith(".spdx.json"):
                    continue
                (directory / name).write_bytes(f"content:{name}".encode())
                digest = release_control.sha256(directory / name)
                (directory / f"{name}.spdx.json").write_text(
                    json.dumps(
                        {
                            "spdxVersion": "SPDX-2.3",
                            "dataLicense": "CC0-1.0",
                            "files": [
                                {
                                    "fileName": name,
                                    "checksums": [
                                        {"algorithm": "SHA256", "checksumValue": digest}
                                    ],
                                }
                            ],
                        }
                    ),
                    encoding="utf-8",
                )
            release_control.prepare_release(
                directory,
                tag="v13.0.0",
                source_sha="1" * 40,
                tag_object_sha="1" * 40,
                automation_sha="2" * 40,
                workflow_url="https://github.com/example/actions/runs/1",
                policy=self.policy,
            )
            report = release_control.verify_assets(
                directory,
                tag="v13.0.0",
                source_sha="1" * 40,
                tag_object_sha="1" * 40,
                policy=self.policy,
                rebuilt=directory,
            )
            self.assertEqual(report["tag"], "v13.0.0")
            self.assertEqual(len(report["assets"]), len(expected) - 1)

    def test_rerun_rejects_changed_binary(self) -> None:
        with tempfile.TemporaryDirectory() as published_temp, tempfile.TemporaryDirectory() as rebuilt_temp:
            published = Path(published_temp)
            rebuilt = Path(rebuilt_temp)
            expected = release_control.expected_asset_names("v13.0.0", self.policy)
            for name in expected:
                if name in {"SOURCE-PROVENANCE.json", "SHA256SUMS"}:
                    continue
                if name.endswith(".spdx.json"):
                    continue
                (published / name).write_bytes(f"content:{name}".encode())
                (rebuilt / name).write_bytes(f"content:{name}".encode())
                digest = release_control.sha256(published / name)
                (published / f"{name}.spdx.json").write_text(
                    json.dumps(
                        {
                            "spdxVersion": "SPDX-2.3",
                            "dataLicense": "CC0-1.0",
                            "files": [
                                {
                                    "fileName": name,
                                    "checksums": [
                                        {"algorithm": "SHA256", "checksumValue": digest}
                                    ],
                                }
                            ],
                        }
                    ),
                    encoding="utf-8",
                )
            release_control.prepare_release(
                published,
                tag="v13.0.0",
                source_sha="1" * 40,
                tag_object_sha="1" * 40,
                automation_sha="2" * 40,
                workflow_url="https://github.com/example/actions/runs/1",
                policy=self.policy,
            )
            changed = rebuilt / "forgejo-runner-13.0.0-linux-amd64"
            changed.write_bytes(b"changed")
            with self.assertRaisesRegex(release_control.ControlError, "immutable rerun mismatch"):
                release_control.verify_assets(
                    published,
                    tag="v13.0.0",
                    source_sha="1" * 40,
                    tag_object_sha="1" * 40,
                    policy=self.policy,
                    rebuilt=rebuilt,
                )

    def test_sync_is_non_destructive_idempotent_and_fail_closed(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "source"
            upstream = root / "upstream.git"
            origin = root / "origin.git"
            checkout = root / "checkout"
            source.mkdir()
            command("git", "init", "-b", "main", cwd=source)
            command("git", "config", "user.name", "Test", cwd=source)
            command("git", "config", "user.email", "test@example.invalid", cwd=source)
            (source / "go.mod").write_text(
                "module code.forgejo.org/forgejo/runner/v13\n\ngo 1.25.0\ntoolchain go1.25.12\n",
                encoding="utf-8",
            )
            command("git", "add", "go.mod", cwd=source)
            command("git", "commit", "-m", "initial", cwd=source)
            command("git", "tag", "v13.0.0", cwd=source)
            command("git", "branch", "support-v12.13.x", cwd=source)
            command("git", "clone", "--bare", str(source), str(upstream), cwd=root)
            command("git", "clone", "--bare", str(source), str(origin), cwd=root)
            command("git", "remote", "add", "upstream", str(upstream), cwd=source)
            (source / "change.txt").write_text("next\n", encoding="utf-8")
            command("git", "add", "change.txt", cwd=source)
            command("git", "commit", "-m", "next", cwd=source)
            command("git", "tag", "v13.0.1", cwd=source)
            command("git", "push", "upstream", "main", "v13.0.1", cwd=source)
            command("git", "clone", str(origin), str(checkout), cwd=root)

            policy = dict(self.policy)
            policy["upstreamUrl"] = str(upstream)
            with mock.patch.object(release_control, "github_releases", return_value=[]):
                dry_run = release_control.sync_repository(
                    checkout,
                    mode="dry-run",
                    origin=str(origin),
                    repository="creator-signal/test",
                    policy=policy,
                    token=None,
                )
                self.assertGreater(dry_run["planned_mutation_count"], 0)
                self.assertEqual(dry_run["missing_stable_releases"], ["v13.0.0", "v13.0.1"])
                self.assertEqual(
                    command("git", "rev-parse", "refs/heads/main", cwd=origin),
                    command("git", "rev-parse", "v13.0.0", cwd=source),
                )
                applied = release_control.sync_repository(
                    checkout,
                    mode="apply",
                    origin=str(origin),
                    repository="creator-signal/test",
                    policy=policy,
                    token=None,
                )
                self.assertGreater(applied["mutation_count"], 0)
                repeated = release_control.sync_repository(
                    checkout,
                    mode="apply",
                    origin=str(origin),
                    repository="creator-signal/test",
                    policy=policy,
                    token=None,
                )
                self.assertEqual(repeated["mutation_count"], 0)

                new_sha = command("git", "rev-parse", "main", cwd=source)
                old_sha = command("git", "rev-parse", "v13.0.0", cwd=source)
                command("git", "--git-dir", str(upstream), "update-ref", "refs/tags/v14.0.0", new_sha, cwd=root)
                command("git", "--git-dir", str(origin), "update-ref", "refs/tags/v14.0.0", old_sha, cwd=root)
                with self.assertRaisesRegex(release_control.ControlError, "immutable tag v14.0.0 differs"):
                    release_control.sync_repository(
                        checkout,
                        mode="apply",
                        origin=str(origin),
                        repository="creator-signal/test",
                        policy=policy,
                        token=None,
                    )


if __name__ == "__main__":
    unittest.main()
