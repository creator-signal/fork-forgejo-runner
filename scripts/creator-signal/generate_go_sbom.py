#!/usr/bin/env python3
"""Generate a deterministic SPDX 2.3 JSON SBOM from Go build metadata."""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from typing import Any


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def spdx_id(value: str) -> str:
    return "SPDXRef-" + re.sub(r"[^A-Za-z0-9.-]+", "-", value).strip("-")


def package(module: dict[str, Any], index: int) -> dict[str, Any]:
    path = module.get("Path", "unknown")
    version = module.get("Version") or "NOASSERTION"
    replacement = module.get("Replace")
    if replacement:
        path = replacement.get("Path", path)
        version = replacement.get("Version") or version
    external_refs = []
    if version != "NOASSERTION":
        purl_path = path.replace("%", "%25").replace("/", "%2F")
        external_refs.append(
            {
                "referenceCategory": "PACKAGE-MANAGER",
                "referenceType": "purl",
                "referenceLocator": f"pkg:golang/{purl_path}@{version}",
            }
        )
    result: dict[str, Any] = {
        "SPDXID": spdx_id(f"Package-{index}-{path}"),
        "name": path,
        "versionInfo": version,
        "downloadLocation": "NOASSERTION",
        "filesAnalyzed": False,
        "licenseConcluded": "NOASSERTION",
        "licenseDeclared": "NOASSERTION",
        "copyrightText": "NOASSERTION",
    }
    if external_refs:
        result["externalRefs"] = external_refs
    return result


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    binary = args.binary.resolve()
    result = subprocess.run(
        ["go", "version", "-m", "-json", str(binary)],
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if result.returncode:
        print(result.stderr, file=sys.stderr)
        return result.returncode
    metadata = json.loads(result.stdout)
    modules = []
    main_module = metadata.get("Main")
    if main_module:
        modules.append(main_module)
    modules.extend(metadata.get("Deps") or [])
    packages = [package(module, index) for index, module in enumerate(modules, 1)]
    binary_digest = file_sha256(binary)
    binary_spdx = spdx_id(f"File-{binary.name}")
    source_epoch = int(os.environ.get("SOURCE_DATE_EPOCH", "0"))
    created = dt.datetime.fromtimestamp(source_epoch, tz=dt.timezone.utc).strftime(
        "%Y-%m-%dT%H:%M:%SZ"
    )
    namespace = (
        "https://github.com/creator-signal/fork-forgejo-runner/sbom/"
        f"{args.tag}/{binary_digest}"
    )
    relationships = [
        {
            "spdxElementId": "SPDXRef-DOCUMENT",
            "relationshipType": "DESCRIBES",
            "relatedSpdxElement": binary_spdx,
        }
    ]
    relationships.extend(
        {
            "spdxElementId": binary_spdx,
            "relationshipType": "GENERATED_FROM",
            "relatedSpdxElement": item["SPDXID"],
        }
        for item in packages
    )
    document = {
        "spdxVersion": "SPDX-2.3",
        "dataLicense": "CC0-1.0",
        "SPDXID": "SPDXRef-DOCUMENT",
        "name": f"{binary.name}-{args.tag}",
        "documentNamespace": namespace,
        "creationInfo": {
            "created": created,
            "creators": ["Tool: creator-signal-generate-go-sbom/1"],
            "comment": f"Exact upstream source commit: {args.source_sha}",
        },
        "files": [
            {
                "SPDXID": binary_spdx,
                "fileName": binary.name,
                "checksums": [
                    {"algorithm": "SHA256", "checksumValue": binary_digest}
                ],
                "licenseConcluded": "NOASSERTION",
                "copyrightText": "NOASSERTION",
            }
        ],
        "packages": packages,
        "relationships": relationships,
    }
    args.output.write_text(
        json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
