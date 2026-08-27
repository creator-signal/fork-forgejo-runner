#!/usr/bin/env python3
"""Apply and verify the governed source backport for a Runner tag."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import sys

import release_control


def fail(message: str) -> None:
    raise release_control.ControlError(message)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--expected-commit", required=True)
    parser.add_argument("--expected-patch-sha256", required=True)
    parser.add_argument("--expected-tree-sha", required=True)
    args = parser.parse_args()
    source = args.source.resolve()
    try:
        policy = release_control.load_policy()
        if release_control.git(source, "rev-parse", "HEAD") != args.source_sha:
            fail("source checkout does not match the planned upstream commit")
        if release_control.git(source, "status", "--porcelain"):
            fail("source checkout is not clean before backport application")
        plan = release_control.source_backport_plan(
            source,
            tag=args.tag,
            source_sha=args.source_sha,
            policy=policy,
        )
        if plan is None:
            fail(f"no governed source backport is configured for {args.tag}")
        expected = {
            "upstreamCommit": args.expected_commit,
            "patchSha256": args.expected_patch_sha256,
            "patchedSourceTreeSha": args.expected_tree_sha,
        }
        for field, value in expected.items():
            if plan[field] != value:
                fail(f"planned source backport {field} mismatch")
        release_control.run(
            ["git", "cherry-pick", "--no-commit", args.expected_commit], cwd=source
        )
        staged_paths = sorted(
            line
            for line in release_control.git(
                source, "diff", "--cached", "--name-only"
            ).splitlines()
            if line
        )
        if staged_paths != plan["changedPaths"]:
            fail("applied source backport path inventory mismatch")
        release_control.run(["git", "diff", "--cached", "--check"], cwd=source)
        actual_tree = release_control.git(source, "write-tree")
        if actual_tree != args.expected_tree_sha:
            fail(
                f"applied source tree mismatch: expected {args.expected_tree_sha}; "
                f"actual {actual_tree}"
            )
        output = os.environ.get("GITHUB_OUTPUT")
        if output:
            with Path(output).open("a", encoding="utf-8") as handle:
                handle.write(f"patched_source_tree_sha={actual_tree}\n")
        print(
            f"Applied governed upstream backport {args.expected_commit} to "
            f"{args.tag}@{args.source_sha}; tree={actual_tree}"
        )
    except release_control.ControlError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
