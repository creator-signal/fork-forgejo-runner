#!/usr/bin/env python3
"""Govern Creator Signal's non-destructive Runner mirror and immutable releases."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request
from typing import Any, Iterable


ROOT = Path(__file__).resolve().parents[2]
POLICY_PATH = ROOT / "creator-signal" / "runner-release-policy.json"


class ControlError(RuntimeError):
    """A fail-closed policy or repository error."""


def run(
    args: list[str],
    *,
    cwd: Path | None = None,
    check: bool = True,
    input_text: str | None = None,
    env: dict[str, str] | None = None,
) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(
        args,
        cwd=cwd,
        check=False,
        input=input_text,
        env=env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if check and result.returncode:
        rendered = " ".join(args)
        raise ControlError(
            f"command failed ({result.returncode}): {rendered}\n"
            f"stdout:\n{result.stdout}\nstderr:\n{result.stderr}"
        )
    return result


def run_bytes(
    args: list[str],
    *,
    cwd: Path | None = None,
    check: bool = True,
    input_bytes: bytes | None = None,
    env: dict[str, str] | None = None,
) -> subprocess.CompletedProcess[bytes]:
    """Run a command without platform newline translation."""
    result = subprocess.run(
        args,
        cwd=cwd,
        check=False,
        input=input_bytes,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if check and result.returncode:
        stdout = result.stdout.decode("utf-8", errors="replace")
        stderr = result.stderr.decode("utf-8", errors="replace")
        raise ControlError(
            f"command failed ({result.returncode}): {' '.join(args)}\n"
            f"stdout:\n{stdout}\nstderr:\n{stderr}"
        )
    return result


def git(repo: Path, *args: str, check: bool = True) -> str:
    return run(["git", *args], cwd=repo, check=check).stdout.strip()


def load_policy(path: Path = POLICY_PATH) -> dict[str, Any]:
    with path.open(encoding="utf-8") as handle:
        policy = json.load(handle)
    required = {
        "upstreamUrl",
        "automationBranch",
        "initialBackfillTag",
        "maintainedBranchPatterns",
        "semanticTagPattern",
        "stableTagPattern",
        "downstreamTagPattern",
        "initialDownstreamReleaseTag",
        "downstreamReleases",
        "sourceBackports",
        "artifacts",
        "supplyChain",
    }
    missing = sorted(required.difference(policy))
    if missing:
        raise ControlError(f"policy is missing required keys: {', '.join(missing)}")
    return policy


def ensure_remote(repo: Path, name: str, url: str) -> None:
    current = git(repo, "remote", "get-url", name, check=False)
    if current:
        if current != url:
            git(repo, "remote", "set-url", name, url)
    else:
        git(repo, "remote", "add", name, url)


def fetch_namespaces(repo: Path, upstream_url: str, origin: str) -> None:
    ensure_remote(repo, "creator-signal-upstream", upstream_url)
    git(
        repo,
        "fetch",
        "--no-tags",
        "--prune",
        "creator-signal-upstream",
        "+refs/heads/*:refs/remotes/creator-signal-upstream/*",
        "+refs/tags/*:refs/creator-signal-upstream-tags/*",
    )
    git(
        repo,
        "fetch",
        "--no-tags",
        "--prune",
        origin,
        "+refs/heads/*:refs/remotes/creator-signal-origin/*",
        "+refs/tags/*:refs/creator-signal-origin-tags/*",
    )


def refs(repo: Path, namespace: str) -> dict[str, str]:
    output = git(
        repo,
        "for-each-ref",
        "--format=%(refname) %(objectname)",
        namespace,
    )
    values: dict[str, str] = {}
    prefix = namespace.rstrip("/") + "/"
    for line in output.splitlines():
        if not line:
            continue
        refname, sha = line.split(" ", 1)
        values[refname.removeprefix(prefix)] = sha
    return values


def commit_sha(repo: Path, ref: str) -> str:
    return git(repo, "rev-parse", f"{ref}^{{commit}}")


def exact_ref_sha(repo: Path, ref: str) -> str:
    return git(repo, "show-ref", "--verify", "--hash", ref, check=False)


def exact_commit_exists(repo: Path, sha: str) -> bool:
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        return False
    result = run(["git", "cat-file", "-e", f"{sha}^{{commit}}"], cwd=repo, check=False)
    return result.returncode == 0


def is_ancestor(repo: Path, ancestor: str, descendant: str) -> bool:
    result = run(
        ["git", "merge-base", "--is-ancestor", ancestor, descendant],
        cwd=repo,
        check=False,
    )
    if result.returncode not in (0, 1):
        raise ControlError(result.stderr.strip() or "git merge-base failed")
    return result.returncode == 0


def github_releases(repository: str, token: str | None) -> list[dict[str, Any]]:
    releases: list[dict[str, Any]] = []
    page = 1
    while True:
        url = f"https://api.github.com/repos/{repository}/releases?per_page=100&page={page}"
        headers = {
            "Accept": "application/vnd.github+json",
            "User-Agent": "creator-signal-runner-release-control/1",
            "X-GitHub-Api-Version": "2022-11-28",
        }
        if token:
            headers["Authorization"] = f"Bearer {token}"
        request = urllib.request.Request(url, headers=headers)
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                batch = json.load(response)
        except urllib.error.HTTPError as error:
            raise ControlError(f"GitHub Releases query failed: HTTP {error.code}") from error
        if not isinstance(batch, list):
            raise ControlError("GitHub Releases response was not a list")
        releases.extend(batch)
        if len(batch) < 100:
            return releases
        page += 1


def expected_asset_names(tag: str, policy: dict[str, Any]) -> list[str]:
    version = tag.removeprefix("v")
    names: list[str] = []
    for artifact in policy["artifacts"]:
        suffix = ".exe" if artifact["os"] == "windows" else ""
        binary = f"forgejo-runner-{version}-{artifact['os']}-{artifact['arch']}{suffix}"
        names.extend((binary, f"{binary}.spdx.json"))
    names.extend(("SOURCE-PROVENANCE.json", "SHA256SUMS"))
    return sorted(names)


def release_is_complete(release: dict[str, Any], tag: str, policy: dict[str, Any]) -> bool:
    if release.get("draft"):
        return False
    present = {asset.get("name") for asset in release.get("assets", [])}
    return set(expected_asset_names(tag, policy)).issubset(present)


def downstream_release_policy(tag: str, policy: dict[str, Any]) -> dict[str, Any] | None:
    configured = policy["downstreamReleases"].get(tag)
    if configured is None:
        return None
    if not re.fullmatch(policy["downstreamTagPattern"], tag):
        raise ControlError(f"downstream release tag is outside policy: {tag}")
    return configured


def release_backport_policy(tag: str, policy: dict[str, Any]) -> dict[str, Any] | None:
    downstream = downstream_release_policy(tag, policy)
    return downstream["backport"] if downstream else policy["sourceBackports"].get(tag)


def write_output(name: str, value: str) -> None:
    output_path = os.environ.get("GITHUB_OUTPUT")
    if not output_path:
        return
    with open(output_path, "a", encoding="utf-8") as handle:
        handle.write(f"{name}={value}\n")


def write_summary(report: dict[str, Any]) -> None:
    summary_path = os.environ.get("GITHUB_STEP_SUMMARY")
    if not summary_path:
        return
    with open(summary_path, "a", encoding="utf-8") as handle:
        handle.write("## Creator Signal Runner upstream synchronization\n\n")
        handle.write(f"- Mode: `{report['mode']}`\n")
        handle.write(f"- Upstream: `{report['upstream_url']}`\n")
        handle.write(f"- Upstream main: `{report['upstream_main_sha']}`\n")
        handle.write(f"- Branch actions: `{len(report['branches'])}`\n")
        handle.write(f"- Tag actions: `{len(report['tags'])}`\n")
        handle.write(f"- Missing completed stable Releases: `{len(report['missing_stable_releases'])}`\n")
        if report["missing_stable_releases"]:
            handle.write("- Missing tags: " + ", ".join(f"`{tag}`" for tag in report["missing_stable_releases"]) + "\n")


def sync_repository(
    repo: Path,
    *,
    mode: str,
    origin: str,
    repository: str,
    policy: dict[str, Any],
    token: str | None,
) -> dict[str, Any]:
    fetch_namespaces(repo, policy["upstreamUrl"], origin)
    upstream_heads = refs(repo, "refs/remotes/creator-signal-upstream")
    origin_heads = refs(repo, "refs/remotes/creator-signal-origin")
    upstream_tags = refs(repo, "refs/creator-signal-upstream-tags")
    origin_tags = refs(repo, "refs/creator-signal-origin-tags")
    branch_patterns = [re.compile(value) for value in policy["maintainedBranchPatterns"]]
    semantic_tag = re.compile(policy["semanticTagPattern"])
    stable_tag = re.compile(policy["stableTagPattern"])

    selected_heads = {
        name: sha
        for name, sha in upstream_heads.items()
        if any(pattern.fullmatch(name) for pattern in branch_patterns)
    }
    if "main" not in selected_heads:
        raise ControlError("authoritative upstream does not expose refs/heads/main")

    branch_actions: list[dict[str, str]] = []
    tag_actions: list[dict[str, str]] = []
    refspecs: list[str] = []
    errors: list[str] = []

    for name, upstream_sha in sorted(selected_heads.items()):
        origin_sha = origin_heads.get(name)
        if origin_sha is None:
            action = "create"
            refspecs.append(
                f"refs/remotes/creator-signal-upstream/{name}:refs/heads/{name}"
            )
        elif origin_sha == upstream_sha:
            action = "unchanged"
        elif is_ancestor(repo, origin_sha, upstream_sha):
            action = "fast-forward"
            refspecs.append(
                f"refs/remotes/creator-signal-upstream/{name}:refs/heads/{name}"
            )
        else:
            action = "reject-non-fast-forward"
            errors.append(
                f"branch {name} is not a fast-forward: GitHub={origin_sha} upstream={upstream_sha}"
            )
        branch_actions.append(
            {
                "name": name,
                "action": action,
                "github_sha": origin_sha or "missing",
                "upstream_sha": upstream_sha,
            }
        )

    selected_tags = {
        name: sha for name, sha in upstream_tags.items() if semantic_tag.fullmatch(name)
    }
    for name, upstream_sha in sorted(selected_tags.items()):
        origin_sha = origin_tags.get(name)
        if origin_sha is None:
            action = "create"
            refspecs.append(
                f"refs/creator-signal-upstream-tags/{name}:refs/tags/{name}"
            )
        elif origin_sha == upstream_sha:
            action = "unchanged"
        else:
            action = "reject-mismatch"
            upstream_commit = commit_sha(
                repo, f"refs/creator-signal-upstream-tags/{name}"
            )
            origin_commit = commit_sha(repo, f"refs/creator-signal-origin-tags/{name}")
            errors.append(
                f"immutable tag {name} differs: GitHub object={origin_sha} commit={origin_commit}; "
                f"upstream object={upstream_sha} commit={upstream_commit}"
            )
        tag_actions.append(
            {
                "name": name,
                "action": action,
                "github_sha": origin_sha or "missing",
                "upstream_sha": upstream_sha,
            }
        )

    downstream_tag_actions: list[dict[str, str]] = []
    for name, configured in sorted(policy["downstreamReleases"].items()):
        if name in upstream_tags:
            errors.append(
                f"reserved Creator Signal downstream tag unexpectedly exists upstream: {name}"
            )
            continue
        expected_sha = configured.get("sourceCommitSha", "")
        if not re.fullmatch(r"[0-9a-f]{40}", expected_sha):
            errors.append(f"downstream tag {name} does not pin a full source commit SHA")
            continue
        origin_sha = origin_tags.get(name)
        if origin_sha is None:
            action = "reserved-unpublished"
        elif origin_sha == expected_sha:
            action = "immutable-downstream"
        else:
            action = "reject-mismatch"
            errors.append(
                f"immutable downstream tag {name} differs: GitHub={origin_sha} expected={expected_sha}"
            )
        downstream_tag_actions.append(
            {
                "name": name,
                "action": action,
                "github_sha": origin_sha or "missing",
                "expected_sha": expected_sha,
            }
        )

    if errors:
        raise ControlError("synchronization rejected before push:\n" + "\n".join(errors))

    releases = github_releases(repository, token)
    releases_by_tag = {release.get("tag_name"): release for release in releases}
    stable_tags = sorted(
        (name for name in selected_tags if stable_tag.fullmatch(name)), key=semver_key
    )
    release_floor = semver_key(policy["initialBackfillTag"])
    release_candidate_tags = [tag for tag in stable_tags if semver_key(tag) >= release_floor]
    missing_stable = [
        tag
        for tag in release_candidate_tags
        if not release_is_complete(releases_by_tag.get(tag, {}), tag, policy)
    ]

    if mode == "apply" and refspecs:
        # One atomic, non-forced push prevents partial refresh. Git also rejects a
        # concurrent non-fast-forward branch or pre-existing tag.
        git(repo, "push", "--atomic", origin, *refspecs)

    report = {
        "schema_version": 1,
        "mode": mode,
        "upstream_url": policy["upstreamUrl"],
        "upstream_main_sha": selected_heads["main"],
        "branches": branch_actions,
        "tags": tag_actions,
        "downstream_tags": downstream_tag_actions,
        "missing_stable_releases": missing_stable,
        "latest_stable_tag": stable_tags[-1] if stable_tags else None,
        "release_floor_tag": policy["initialBackfillTag"],
        "mutation_count": len(refspecs) if mode == "apply" else 0,
        "planned_mutation_count": len(refspecs),
    }
    return report


def semver_key(tag: str) -> tuple[int, int, int, str]:
    match = re.fullmatch(r"v(\d+)\.(\d+)\.(\d+)(?:-(.+))?", tag)
    if not match:
        raise ControlError(f"not a semantic version tag: {tag}")
    major, minor, patch, prerelease = match.groups()
    return int(major), int(minor), int(patch), prerelease or "~"


def plan_tag(
    repo: Path,
    *,
    tag: str,
    origin: str,
    repository: str,
    policy: dict[str, Any],
    token: str | None,
) -> dict[str, Any]:
    if not re.fullmatch(policy["semanticTagPattern"], tag):
        raise ControlError(f"tag is outside the governed semantic-version policy: {tag}")
    fetch_namespaces(repo, policy["upstreamUrl"], origin)
    downstream = downstream_release_policy(tag, policy)
    if downstream:
        base_tag = downstream.get("baseTag", "")
        base_ref = f"refs/creator-signal-upstream-tags/{base_tag}"
        mirrored_base_ref = f"refs/creator-signal-origin-tags/{base_tag}"
        upstream_base_object = exact_ref_sha(repo, base_ref)
        mirrored_base_object = exact_ref_sha(repo, mirrored_base_ref)
        if not upstream_base_object or upstream_base_object != mirrored_base_object:
            raise ControlError(
                f"downstream base tag is absent or mismatched: {base_tag}; "
                f"GitHub={mirrored_base_object or 'missing'} upstream={upstream_base_object or 'missing'}"
            )
        base_source_sha = commit_sha(repo, base_ref)
        if base_source_sha != downstream.get("baseSourceSha"):
            raise ControlError(
                f"{tag} base source mismatch: policy={downstream.get('baseSourceSha')} actual={base_source_sha}"
            )
        backport = source_backport_plan(
            repo,
            tag=tag,
            source_sha=base_source_sha,
            policy=policy,
            configured=downstream["backport"],
        )
        if not backport:
            raise ControlError(f"{tag} does not define its required downstream backport")
        if backport["patchSha256"] != downstream["backport"].get("patchSha256"):
            raise ControlError(f"{tag} downstream patch digest does not reproduce policy")
        source_sha = downstream.get("sourceCommitSha", "")
        if not exact_commit_exists(repo, source_sha):
            raise ControlError(f"{tag} committed downstream source is unavailable: {source_sha}")
        parents = git(repo, "rev-list", "--parents", "-n", "1", source_sha).split()
        if parents != [source_sha, base_source_sha]:
            raise ControlError(
                f"{tag} source commit must have only the exact upstream base parent: {base_source_sha}"
            )
        source_tree_sha = git(repo, "rev-parse", f"{source_sha}^{{tree}}")
        if source_tree_sha != downstream.get("sourceTreeSha") or source_tree_sha != backport["patchedSourceTreeSha"]:
            raise ControlError(f"{tag} committed source tree does not reproduce the governed patch")
        subject = git(repo, "show", "-s", "--format=%s", source_sha)
        if subject != downstream.get("sourceCommitSubject"):
            raise ControlError(f"{tag} committed source subject differs from policy")
        upstream_downstream_ref = f"refs/creator-signal-upstream-tags/{tag}"
        if exact_ref_sha(repo, upstream_downstream_ref):
            raise ControlError(f"Creator Signal downstream tag collides with upstream: {tag}")
        origin_ref = f"refs/creator-signal-origin-tags/{tag}"
        origin_object = exact_ref_sha(repo, origin_ref)
        if origin_object and origin_object != source_sha:
            raise ControlError(
                f"immutable downstream tag mismatch for {tag}: GitHub={origin_object} expected={source_sha}"
            )
        tag_object_sha = source_sha
        tag_exists = bool(origin_object)
        source_checkout_ref = source_sha
        source_checkout_sha = source_sha
        source_is_committed = True
        source_tree_sha = downstream["sourceTreeSha"]
        go_mod = git(repo, "show", f"{source_sha}:go.mod")
    else:
        base_tag = ""
        base_source_sha = ""
        upstream_ref = f"refs/creator-signal-upstream-tags/{tag}"
        origin_ref = f"refs/creator-signal-origin-tags/{tag}"
        upstream_object = exact_ref_sha(repo, upstream_ref)
        origin_object = exact_ref_sha(repo, origin_ref)
        if not upstream_object:
            raise ControlError(f"upstream tag does not exist: {tag}")
        if not origin_object:
            raise ControlError(f"GitHub mirror tag does not exist; apply synchronization first: {tag}")
        if upstream_object != origin_object:
            raise ControlError(
                f"immutable tag mismatch for {tag}: GitHub={origin_object} upstream={upstream_object}"
            )
        source_sha = commit_sha(repo, upstream_ref)
        backport = source_backport_plan(
            repo,
            tag=tag,
            source_sha=source_sha,
            policy=policy,
        )
        source_tree_sha = git(repo, "rev-parse", f"{source_sha}^{{tree}}")
        tag_object_sha = upstream_object
        tag_exists = True
        source_checkout_ref = tag
        source_checkout_sha = source_sha
        source_is_committed = False
        go_mod = git(repo, "show", f"{upstream_ref}:go.mod")
    module_match = re.search(r"(?m)^module\s+(\S+)\s*$", go_mod)
    go_match = re.search(r"(?m)^go\s+(\S+)\s*$", go_mod)
    toolchain_match = re.search(r"(?m)^toolchain\s+go(\S+)\s*$", go_mod)
    if not module_match or not go_match:
        raise ControlError(f"{tag} go.mod does not declare module and Go version")
    go_version = toolchain_match.group(1) if toolchain_match else go_match.group(1)
    releases = github_releases(repository, token)
    release = next((item for item in releases if item.get("tag_name") == tag), None)
    report = {
        "schema_version": 1,
        "upstream_url": policy["upstreamUrl"],
        "tag": tag,
        "version": tag.removeprefix("v"),
        "tag_object_sha": tag_object_sha,
        "tag_exists": tag_exists,
        "source_sha": source_sha,
        "source_tree_sha": source_tree_sha,
        "source_checkout_ref": source_checkout_ref,
        "source_checkout_sha": source_checkout_sha,
        "source_is_committed": source_is_committed,
        "base_tag": base_tag,
        "base_source_sha": base_source_sha,
        "backport_required": backport is not None and not source_is_committed,
        "backport_commit": backport["upstreamCommit"] if backport else "",
        "backport_pull_request": backport["upstreamPullRequest"] if backport else "",
        "backport_patch_sha256": backport["patchSha256"] if backport else "",
        "patched_source_tree_sha": (
            backport["patchedSourceTreeSha"] if backport else source_tree_sha
        ),
        "source_backport": backport,
        "module_path": module_match.group(1),
        "go_version": go_version,
        "prerelease": not bool(re.fullmatch(policy["stableTagPattern"], tag)),
        "release_exists": release is not None,
        "release_complete": release_is_complete(release or {}, tag, policy),
        "release_draft": bool(release and release.get("draft")),
        "release_url": release.get("html_url") if release else None,
        "expected_assets": expected_asset_names(tag, policy),
    }
    return report


def publish_downstream_tag(
    repo: Path,
    *,
    tag: str,
    origin: str,
    repository: str,
    policy: dict[str, Any],
    token: str | None,
) -> dict[str, Any]:
    if not downstream_release_policy(tag, policy):
        raise ControlError(f"refusing to publish non-downstream tag with this command: {tag}")
    plan = plan_tag(
        repo,
        tag=tag,
        origin=origin,
        repository=repository,
        policy=policy,
        token=token,
    )
    if plan["tag_exists"]:
        return {"tag": tag, "source_sha": plan["source_sha"], "mutation_count": 0}
    git(repo, "push", origin, f"{plan['source_sha']}:refs/tags/{tag}")
    remote = git(repo, "ls-remote", "--tags", origin, f"refs/tags/{tag}")
    if remote.split()[:1] != [plan["source_sha"]]:
        raise ControlError(f"published downstream tag did not resolve to expected source: {tag}")
    return {"tag": tag, "source_sha": plan["source_sha"], "mutation_count": 1}


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def source_backport_plan(
    repo: Path,
    *,
    tag: str,
    source_sha: str,
    policy: dict[str, Any],
    configured: dict[str, Any] | None = None,
) -> dict[str, Any] | None:
    configured = configured if configured is not None else policy["sourceBackports"].get(tag)
    if configured is None:
        return None
    commit = configured.get("upstreamCommit", "")
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ControlError(f"{tag} source backport does not pin a full upstream commit SHA")
    resolved = git(repo, "rev-parse", f"{commit}^{{commit}}", check=False)
    if resolved != commit:
        raise ControlError(f"{tag} source backport commit is unavailable or mismatched: {commit}")
    parents = git(repo, "rev-list", "--parents", "-n", "1", commit).split()
    if len(parents) != 2:
        raise ControlError(f"{tag} source backport must be a single-parent upstream commit")
    if not is_ancestor(repo, source_sha, commit):
        raise ControlError(f"{tag} source backport is not descended from source {source_sha}")
    changed_paths = sorted(
        line
        for line in git(
            repo,
            "diff-tree",
            "--no-commit-id",
            "--name-only",
            "-r",
            commit,
        ).splitlines()
        if line
    )
    expected_paths = sorted(configured.get("changedPaths", []))
    if changed_paths != expected_paths:
        raise ControlError(
            f"{tag} source backport path mismatch: expected={expected_paths}; actual={changed_paths}"
        )
    patch = run_bytes(
        ["git", "diff", "--binary", parents[1], commit], cwd=repo
    ).stdout
    patch_sha256 = hashlib.sha256(patch).hexdigest()
    expected_patch_sha256 = configured.get("patchSha256")
    if expected_patch_sha256 and patch_sha256 != expected_patch_sha256:
        raise ControlError(
            f"{tag} source backport patch mismatch: expected={expected_patch_sha256}; actual={patch_sha256}"
        )
    with tempfile.TemporaryDirectory(prefix="creator-signal-runner-backport-") as temporary:
        index_path = str(Path(temporary) / "index")
        git_env = dict(os.environ)
        git_env["GIT_INDEX_FILE"] = index_path
        run(["git", "read-tree", source_sha], cwd=repo, env=git_env)
        run_bytes(
            ["git", "apply", "--cached", "--whitespace=error-all", "-"],
            cwd=repo,
            input_bytes=patch,
            env=git_env,
        )
        patched_tree_sha = run(["git", "write-tree"], cwd=repo, env=git_env).stdout.strip()
    return {
        "upstreamCommit": commit,
        "upstreamPullRequest": configured["upstreamPullRequest"],
        "reason": configured["reason"],
        "changedPaths": changed_paths,
        "patchSha256": patch_sha256,
        "patchedSourceTreeSha": patched_tree_sha,
    }


def prepare_release(
    directory: Path,
    *,
    tag: str,
    source_sha: str,
    tag_object_sha: str,
    base_tag: str,
    base_source_sha: str,
    automation_sha: str,
    workflow_url: str,
    backport_commit: str,
    backport_patch_sha256: str,
    patched_source_tree_sha: str,
    policy: dict[str, Any],
) -> None:
    configured_backport = release_backport_policy(tag, policy)
    downstream = downstream_release_policy(tag, policy)
    expected_base_tag = downstream["baseTag"] if downstream else ""
    expected_base_sha = downstream["baseSourceSha"] if downstream else ""
    if base_tag != expected_base_tag or base_source_sha != expected_base_sha:
        raise ControlError(
            f"upstream base identity mismatch: expected {expected_base_tag or 'none'}@{expected_base_sha or 'none'}; "
            f"received {base_tag or 'none'}@{base_source_sha or 'none'}"
        )
    expected_commit = configured_backport["upstreamCommit"] if configured_backport else ""
    if backport_commit != expected_commit:
        raise ControlError(
            f"source backport mismatch: expected {expected_commit or 'none'}; "
            f"received {backport_commit or 'none'}"
        )
    if configured_backport:
        if not re.fullmatch(r"[0-9a-f]{64}", backport_patch_sha256):
            raise ControlError("source backport patch SHA-256 is invalid")
        expected_patch = configured_backport.get("patchSha256")
        if expected_patch and backport_patch_sha256 != expected_patch:
            raise ControlError("source backport patch SHA-256 differs from release policy")
    elif backport_patch_sha256:
        raise ControlError("unexpected source backport patch SHA-256")
    if not re.fullmatch(r"[0-9a-f]{40}", patched_source_tree_sha):
        raise ControlError("patched source tree SHA is invalid")
    if downstream and patched_source_tree_sha != downstream["sourceTreeSha"]:
        raise ControlError("downstream source tree differs from release policy")
    expected = expected_asset_names(tag, policy)
    content_assets = [name for name in expected if name not in {"SHA256SUMS", "SOURCE-PROVENANCE.json"}]
    missing = [name for name in content_assets if not (directory / name).is_file()]
    if missing:
        raise ControlError(f"cannot prepare release; assets are missing: {', '.join(missing)}")
    provenance = {
        "schemaVersion": 2,
        "upstreamUrl": policy["upstreamUrl"],
        "sourceTag": tag,
        "sourceTagObjectSha": tag_object_sha,
        "sourceSha": source_sha,
        "upstreamBaseTag": base_tag or None,
        "upstreamBaseSha": base_source_sha or None,
        "patchedSourceTreeSha": patched_source_tree_sha,
        "sourceBackport": (
            {
                "upstreamCommit": backport_commit,
                "upstreamPullRequest": configured_backport["upstreamPullRequest"],
                "reason": configured_backport["reason"],
                "patchSha256": backport_patch_sha256,
                "changedPaths": sorted(configured_backport["changedPaths"]),
                **(
                    {"baseTag": base_tag, "baseSha": base_source_sha}
                    if base_tag
                    else {}
                ),
            }
            if configured_backport
            else None
        ),
        "automationBranch": policy["automationBranch"],
        "automationSha": automation_sha,
        "workflowUrl": workflow_url,
        "windowsSupport": "Creator Signal-tested; upstream-unsupported",
        "authenticode": policy["supplyChain"]["authenticode"],
        "artifacts": {name: sha256(directory / name) for name in sorted(content_assets)},
    }
    provenance_path = directory / "SOURCE-PROVENANCE.json"
    provenance_path.write_text(
        json.dumps(provenance, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    checksummed = sorted(content_assets + [provenance_path.name])
    checksum_path = directory / "SHA256SUMS"
    checksum_path.write_text(
        "".join(f"{sha256(directory / name)}  {name}\n" for name in checksummed),
        encoding="utf-8",
    )


def read_checksums(path: Path) -> dict[str, str]:
    checksums: dict[str, str] = {}
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        match = re.fullmatch(r"([0-9a-f]{64})  ([^/\\]+)", line)
        if not match:
            raise ControlError(f"invalid checksum line {number}: {line!r}")
        digest, name = match.groups()
        if name in checksums:
            raise ControlError(f"duplicate checksum entry: {name}")
        checksums[name] = digest
    return checksums


def verify_assets(
    directory: Path,
    *,
    tag: str,
    source_sha: str,
    tag_object_sha: str,
    base_tag: str,
    base_source_sha: str,
    backport_commit: str,
    backport_patch_sha256: str,
    patched_source_tree_sha: str,
    policy: dict[str, Any],
    rebuilt: Path | None,
) -> dict[str, Any]:
    expected = expected_asset_names(tag, policy)
    present = sorted(path.name for path in directory.iterdir() if path.is_file())
    if present != expected:
        raise ControlError(f"asset set mismatch: expected={expected}; present={present}")
    checksums = read_checksums(directory / "SHA256SUMS")
    checksum_expected = set(expected).difference({"SHA256SUMS"})
    if set(checksums) != checksum_expected:
        raise ControlError(
            f"checksum coverage mismatch: expected={sorted(checksum_expected)}; present={sorted(checksums)}"
        )
    for name, expected_digest in checksums.items():
        actual = sha256(directory / name)
        if actual != expected_digest:
            raise ControlError(
                f"checksum mismatch for {name}: recorded={expected_digest} actual={actual}"
            )
    provenance = json.loads((directory / "SOURCE-PROVENANCE.json").read_text(encoding="utf-8"))
    if provenance.get("sourceTag") != tag or provenance.get("sourceSha") != source_sha:
        raise ControlError(
            f"source provenance mismatch: expected {tag}@{source_sha}; "
            f"recorded {provenance.get('sourceTag')}@{provenance.get('sourceSha')}"
        )
    if provenance.get("sourceTagObjectSha") != tag_object_sha:
        raise ControlError(
            f"tag object provenance mismatch: expected {tag_object_sha}; "
            f"recorded {provenance.get('sourceTagObjectSha')}"
        )
    downstream = downstream_release_policy(tag, policy)
    expected_base_tag = downstream["baseTag"] if downstream else ""
    expected_base_sha = downstream["baseSourceSha"] if downstream else ""
    if base_tag != expected_base_tag or base_source_sha != expected_base_sha:
        raise ControlError("requested upstream base identity differs from release policy")
    if provenance.get("upstreamBaseTag") != (base_tag or None) or provenance.get(
        "upstreamBaseSha"
    ) != (base_source_sha or None):
        raise ControlError("upstream base provenance mismatch")
    expected_backport = release_backport_policy(tag, policy)
    recorded_backport = provenance.get("sourceBackport")
    if expected_backport:
        expected_record = {
            "upstreamCommit": backport_commit,
            "upstreamPullRequest": expected_backport["upstreamPullRequest"],
            "reason": expected_backport["reason"],
            "patchSha256": backport_patch_sha256,
            "changedPaths": sorted(expected_backport["changedPaths"]),
            **(
                {"baseTag": base_tag, "baseSha": base_source_sha}
                if base_tag
                else {}
            ),
        }
        if backport_commit != expected_backport["upstreamCommit"] or recorded_backport != expected_record:
            raise ControlError("source backport provenance mismatch")
    elif backport_commit or backport_patch_sha256 or recorded_backport is not None:
        raise ControlError("unexpected source backport provenance")
    if provenance.get("patchedSourceTreeSha") != patched_source_tree_sha:
        raise ControlError("patched source tree provenance mismatch")
    if provenance.get("upstreamUrl") != policy["upstreamUrl"]:
        raise ControlError("source provenance upstream URL mismatch")
    content_assets = set(expected).difference({"SHA256SUMS", "SOURCE-PROVENANCE.json"})
    if set(provenance.get("artifacts", {})) != content_assets:
        raise ControlError("source provenance artifact inventory mismatch")
    for name, digest in provenance.get("artifacts", {}).items():
        if checksums.get(name) != digest:
            raise ControlError(f"provenance digest mismatch for {name}")
    binaries = [name for name in expected if ".spdx.json" not in name and name.startswith("forgejo-runner-")]
    for binary in binaries:
        sbom_name = f"{binary}.spdx.json"
        sbom = json.loads((directory / sbom_name).read_text(encoding="utf-8"))
        if sbom.get("spdxVersion") != "SPDX-2.3" or sbom.get("dataLicense") != "CC0-1.0":
            raise ControlError(f"invalid SPDX document contract: {sbom_name}")
        described_file = next(
            (item for item in sbom.get("files", []) if item.get("fileName") == binary),
            None,
        )
        if not described_file:
            raise ControlError(f"SBOM does not describe released binary: {sbom_name}")
        recorded = next(
            (
                item.get("checksumValue")
                for item in described_file.get("checksums", [])
                if item.get("algorithm") == "SHA256"
            ),
            None,
        )
        if recorded != checksums[binary]:
            raise ControlError(f"SBOM binary digest mismatch: {sbom_name}")
        comment = sbom.get("creationInfo", {}).get("comment", "")
        expected_comment = sbom_source_comment(
            source_sha=source_sha,
            base_tag=base_tag,
            base_source_sha=base_source_sha,
            backport_commit=backport_commit,
            backport_patch_sha256=backport_patch_sha256,
            patched_source_tree_sha=patched_source_tree_sha,
        )
        if comment != expected_comment:
            raise ControlError(f"SBOM source provenance mismatch: {sbom_name}")
    if rebuilt:
        for name in binaries:
            rebuilt_path = rebuilt / name
            if not rebuilt_path.is_file():
                raise ControlError(f"rebuilt binary is missing: {name}")
            rebuilt_digest = sha256(rebuilt_path)
            if rebuilt_digest != checksums[name]:
                raise ControlError(
                    f"immutable rerun mismatch for {name}: published={checksums[name]} rebuilt={rebuilt_digest}"
                )
    return {"tag": tag, "source_sha": source_sha, "assets": checksums}


def sbom_source_comment(
    *,
    source_sha: str,
    base_tag: str,
    base_source_sha: str,
    backport_commit: str,
    backport_patch_sha256: str,
    patched_source_tree_sha: str,
) -> str:
    if base_tag:
        return (
            f"Exact downstream source commit: {source_sha}; "
            f"upstream base: {base_tag}@{base_source_sha}; "
            f"source backport commit: {backport_commit or 'none'}; "
            f"backport patch SHA-256: {backport_patch_sha256 or 'none'}; "
            f"source tree: {patched_source_tree_sha}"
        )
    return (
        f"Exact upstream source commit: {source_sha}; "
        f"source backport commit: {backport_commit or 'none'}; "
        f"backport patch SHA-256: {backport_patch_sha256 or 'none'}; "
        f"patched source tree: {patched_source_tree_sha}"
    )


def release_body(
    tag: str,
    source_sha: str,
    base_tag: str,
    base_source_sha: str,
    workflow_url: str,
    backport_commit: str,
    backport_pull_request: str,
    backport_patch_sha256: str,
    patched_source_tree_sha: str,
) -> str:
    version = tag.removeprefix("v")
    return f"""Creator Signal downstream build of Forgejo Runner {tag}.

Creator Signal downstream source tag: `{tag}`
Exact downstream source commit: `{source_sha}`
Authoritative upstream base: https://code.forgejo.org/forgejo/runner/src/tag/{base_tag}
Exact upstream base commit: `{base_source_sha}`
Upstream release notes: https://code.forgejo.org/forgejo/runner/releases/tag/{base_tag}
Build workflow: {workflow_url}
Creator Signal source correction: upstream commit `{backport_commit}` ({backport_pull_request})
Backport patch SHA-256: `{backport_patch_sha256}`
Patched source tree: `{patched_source_tree_sha}`

Published executables:
- `forgejo-runner-{version}-linux-amd64`
- `forgejo-runner-{version}-linux-arm64`
- `forgejo-runner-{version}-windows-amd64.exe`

The Windows executable is built and tested natively by Creator Signal, does not require WSL, and remains unsupported by upstream Forgejo. Authenticode signing is not included because no governed Windows code-signing identity is authorized. `SHA256SUMS`, SPDX JSON SBOMs, source provenance metadata, and GitHub keyless attestations are published for independent verification.
"""


def write_json(path: Path, value: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def parse_args(argv: Iterable[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--policy", type=Path, default=POLICY_PATH)
    subparsers = parser.add_subparsers(dest="command", required=True)

    sync_parser = subparsers.add_parser("sync")
    sync_parser.add_argument("--mode", choices=("dry-run", "apply"), required=True)
    sync_parser.add_argument("--repo", type=Path, default=Path.cwd())
    sync_parser.add_argument("--origin", default="origin")
    sync_parser.add_argument("--repository", required=True)
    sync_parser.add_argument("--report", type=Path, required=True)

    plan_parser = subparsers.add_parser("plan-tag")
    plan_parser.add_argument("--tag", required=True)
    plan_parser.add_argument("--repo", type=Path, default=Path.cwd())
    plan_parser.add_argument("--origin", default="origin")
    plan_parser.add_argument("--repository", required=True)
    plan_parser.add_argument("--report", type=Path, required=True)

    publish_tag_parser = subparsers.add_parser("publish-downstream-tag")
    publish_tag_parser.add_argument("--tag", required=True)
    publish_tag_parser.add_argument("--repo", type=Path, default=Path.cwd())
    publish_tag_parser.add_argument("--origin", default="origin")
    publish_tag_parser.add_argument("--repository", required=True)
    publish_tag_parser.add_argument("--report", type=Path, required=True)

    prepare_parser = subparsers.add_parser("prepare-release")
    prepare_parser.add_argument("--directory", type=Path, required=True)
    prepare_parser.add_argument("--tag", required=True)
    prepare_parser.add_argument("--source-sha", required=True)
    prepare_parser.add_argument("--tag-object-sha", required=True)
    prepare_parser.add_argument("--base-tag", default="")
    prepare_parser.add_argument("--base-source-sha", default="")
    prepare_parser.add_argument("--automation-sha", required=True)
    prepare_parser.add_argument("--workflow-url", required=True)
    prepare_parser.add_argument("--backport-commit", required=True)
    prepare_parser.add_argument("--backport-patch-sha256", required=True)
    prepare_parser.add_argument("--patched-source-tree-sha", required=True)

    verify_parser = subparsers.add_parser("verify-assets")
    verify_parser.add_argument("--directory", type=Path, required=True)
    verify_parser.add_argument("--rebuilt", type=Path)
    verify_parser.add_argument("--tag", required=True)
    verify_parser.add_argument("--source-sha", required=True)
    verify_parser.add_argument("--tag-object-sha", required=True)
    verify_parser.add_argument("--base-tag", default="")
    verify_parser.add_argument("--base-source-sha", default="")
    verify_parser.add_argument("--backport-commit", required=True)
    verify_parser.add_argument("--backport-patch-sha256", required=True)
    verify_parser.add_argument("--patched-source-tree-sha", required=True)

    body_parser = subparsers.add_parser("release-body")
    body_parser.add_argument("--tag", required=True)
    body_parser.add_argument("--source-sha", required=True)
    body_parser.add_argument("--base-tag", required=True)
    body_parser.add_argument("--base-source-sha", required=True)
    body_parser.add_argument("--workflow-url", required=True)
    body_parser.add_argument("--backport-commit", required=True)
    body_parser.add_argument("--backport-pull-request", required=True)
    body_parser.add_argument("--backport-patch-sha256", required=True)
    body_parser.add_argument("--patched-source-tree-sha", required=True)
    body_parser.add_argument("--output", type=Path, required=True)
    return parser.parse_args(list(argv))


def main(argv: Iterable[str] = sys.argv[1:]) -> int:
    args = parse_args(argv)
    policy = load_policy(args.policy)
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN")
    try:
        if args.command == "sync":
            report = sync_repository(
                args.repo.resolve(),
                mode=args.mode,
                origin=args.origin,
                repository=args.repository,
                policy=policy,
                token=token,
            )
            write_json(args.report, report)
            write_output("upstream_main_sha", report["upstream_main_sha"])
            write_output("latest_stable_tag", report["latest_stable_tag"] or "")
            write_output(
                "missing_stable_tags_json",
                json.dumps(report["missing_stable_releases"], separators=(",", ":")),
            )
            write_output("planned_mutation_count", str(report["planned_mutation_count"]))
            write_output("mutation_count", str(report["mutation_count"]))
            write_summary(report)
        elif args.command == "plan-tag":
            report = plan_tag(
                args.repo.resolve(),
                tag=args.tag,
                origin=args.origin,
                repository=args.repository,
                policy=policy,
                token=token,
            )
            write_json(args.report, report)
            for name in (
                "source_sha",
                "source_tree_sha",
                "source_checkout_ref",
                "source_checkout_sha",
                "source_is_committed",
                "base_tag",
                "base_source_sha",
                "backport_required",
                "backport_commit",
                "backport_pull_request",
                "backport_patch_sha256",
                "patched_source_tree_sha",
                "version",
                "tag_object_sha",
                "tag_exists",
                "module_path",
                "go_version",
                "prerelease",
                "release_exists",
                "release_complete",
                "release_draft",
            ):
                write_output(name, str(report[name]).lower() if isinstance(report[name], bool) else str(report[name]))
        elif args.command == "publish-downstream-tag":
            report = publish_downstream_tag(
                args.repo.resolve(),
                tag=args.tag,
                origin=args.origin,
                repository=args.repository,
                policy=policy,
                token=token,
            )
            write_json(args.report, report)
            write_output("mutation_count", str(report["mutation_count"]))
        elif args.command == "prepare-release":
            prepare_release(
                args.directory.resolve(),
                tag=args.tag,
                source_sha=args.source_sha,
                tag_object_sha=args.tag_object_sha,
                base_tag=args.base_tag,
                base_source_sha=args.base_source_sha,
                automation_sha=args.automation_sha,
                workflow_url=args.workflow_url,
                backport_commit=args.backport_commit,
                backport_patch_sha256=args.backport_patch_sha256,
                patched_source_tree_sha=args.patched_source_tree_sha,
                policy=policy,
            )
        elif args.command == "verify-assets":
            report = verify_assets(
                args.directory.resolve(),
                tag=args.tag,
                source_sha=args.source_sha,
                tag_object_sha=args.tag_object_sha,
                base_tag=args.base_tag,
                base_source_sha=args.base_source_sha,
                backport_commit=args.backport_commit,
                backport_patch_sha256=args.backport_patch_sha256,
                patched_source_tree_sha=args.patched_source_tree_sha,
                policy=policy,
                rebuilt=args.rebuilt.resolve() if args.rebuilt else None,
            )
            print(json.dumps(report, indent=2, sort_keys=True))
        elif args.command == "release-body":
            args.output.write_text(
                release_body(
                    args.tag,
                    args.source_sha,
                    args.base_tag,
                    args.base_source_sha,
                    args.workflow_url,
                    args.backport_commit,
                    args.backport_pull_request,
                    args.backport_patch_sha256,
                    args.patched_source_tree_sha,
                ),
                encoding="utf-8",
            )
    except ControlError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
