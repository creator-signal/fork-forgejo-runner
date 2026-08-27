# Creator Signal Forgejo Runner mirror and release runbook

This is a downstream Creator Signal automation projection for the authoritative
Forgejo Runner repository at
`https://code.forgejo.org/forgejo/runner.git`. It is not an upstream Forgejo
contribution and does not change the Runner source carried by mirrored upstream
branches or tags. A release policy may, however, name a minimal upstream fix to
apply after checking out a tag; that correction is never written to a mirrored
ref and is disclosed in every release provenance record and SBOM.

The protected default and integration branch is
`creator-signal/automation`. Upstream `main`, maintained `support-v*.x`
branches, and semantic-version tags remain clean mirror refs. Automation files
exist only on the protected branch and Issue branches derived from it.

## Security and authorization boundary

- The default workflow permission is `contents: read`.
- Source-derived builds and tests have read-only repository access and no OIDC,
  attestation, or Release permission.
- Only the final job of an explicitly publishing release run receives
  `contents: write`, `id-token: write`, and `attestations: write`. It does not
  execute the built binaries.
- Synchronization uses anonymous upstream Git reads and the job-scoped GitHub
  token. No long-lived Codeberg or Forgejo credential is required.
- Every external Action reference is a full commit SHA. Workflow validation
  also runs checksum-pinned `actionlint`, deterministic contract tests, and
  pinned `zizmor` analysis.
- Existing branches are only fast-forwarded, tags are immutable, and the
  synchronization push is atomic. There is no automated deletion or forced
  update.
- Native Windows amd64 is Creator Signal-tested but upstream-unsupported. The
  runner's `internal/...` packages are tested and the binary is built and
  smoke-tested under PowerShell on a native Windows GitHub-hosted runner. It
  does not require WSL or a container runtime. The broader container-capable
  `act/...` suite remains in the complete Linux amd64 test gate. Authenticode
  remains out of scope until a governed code-signing identity is separately
  authorized.

This automation does **not** register a Runner; deploy or restart Forgejo;
change Sales Pulse, Coolify, Zot, DEV, STAGE, or PROD; rotate credentials;
expose a service; or publish to Docker Hub or S3.

## Scheduled refresh and manual dry-run

The `Governed upstream synchronization` workflow runs daily at 04:37 UTC. A
scheduled run performs a read-only plan first and applies the same plan only if
the plan succeeds. It records the authoritative upstream `main` SHA, every
selected branch/tag disposition, and all stable tags without a complete Creator
Signal GitHub Release at or above the governed initial `v13.0.0` backfill
floor. Older tags are still mirrored and protected from mismatch, but are not
implicitly selected for downstream Release backfill.

Before any manual mutation, run the workflow with `mode=dry-run`:

```sh
gh workflow run upstream-sync.yml \
  --repo creator-signal/fork-forgejo-runner \
  --ref creator-signal/automation \
  -f mode=dry-run
```

Inspect the completed run and its `upstream-sync-dry-run-*` report. To apply the
same governed refresh, rerun with `mode=apply`. An apply rerun with no upstream
change reports zero mutations and succeeds.

## Mismatch and divergence recovery

The workflow fails before pushing anything when:

- an existing GitHub tag object differs from the same upstream tag object; or
- a maintained GitHub branch cannot fast-forward to upstream.

The error records both object SHAs and, for tags, both peeled commit SHAs. Do
not force-push, delete, or recreate the ref. Confirm the authoritative upstream
URL and inspect the GitHub audit log and ref history. Record the reconciliation
decision on the owning Issue. A conflicting immutable tag requires a new,
explicit version or repository-owner intervention; automation must never make
that decision silently.

## Qualification and named backfill

### Governed v13.0.0 PTY correction

The unchanged `v13.0.0` tag drops chunks from high-throughput host/LXC command
output because those executor subprocesses run in a PTY. Upstream corrected the
defect after the tag in commit
`d4db4179a9ba6a0d07e63b8cf382d90fccb2ff21` ([upstream PR #1692](https://code.forgejo.org/forgejo/runner/pulls/1692)).
Creator Signal therefore fails closed on an exact minimal backport for this one
tag: the policy pins the upstream commit and its seven changed paths, computes
the patch SHA-256 and resulting Git tree, and requires Linux amd64, Linux arm64,
and Windows amd64 to apply and reproduce that same tree before testing or
building. The full race-enabled Linux suite and every LXC case remain enabled;
no test is serialized, filtered, skipped, or reclassified as passing.

`SOURCE-PROVENANCE.json`, all SPDX SBOMs, and the release notes retain the base
tag/SHA, upstream fix commit/PR, patch digest, and patched tree. A changed
upstream commit, path inventory, patch, or output tree stops qualification.

Every automation PR qualifies the Issue's initial acceptance tag (`v13.0.0`)
without publication. The reusable, read-only qualification workflow verifies
one exact mirrored tag/SHA, derives the module path and exact Go toolchain from
that tag's `go.mod`, then runs:

- Linux amd64 native unit/container-capable tests after preparing LXC with the
  upstream helpers, followed by deterministic build and version/configuration
  smoke checks. Before Bookworm creation, the GitHub-hosted Ubuntu image's LXC
  Debian template is seeded from the installed Bookworm archive keyring, so
  archive verification cannot fall back to the template's obsolete Jessie
  bootstrap key. GitHub-hosted Ubuntu also arrives with Docker firewall
  isolation that can leave `lxcbr0` unable to reach its package mirrors. The
  job derives and validates the bridge's single private `/24` and the runner's
  single IPv4 egress interface, then idempotently permits only outbound traffic
  from that bridge, established/related return traffic, and IPv4 masquerading
  for that exact subnet/interface pair. It never flushes firewall tables,
  changes a global forwarding policy, disables Docker isolation, or enables
  unrestricted IPv6 forwarding;
- Linux arm64 native deterministic build and version/configuration smoke
  checks; and
- native Windows amd64 `internal/...` runner tests with container features
  disabled, followed by deterministic build and version/configuration smoke
  checks in PowerShell. The `act/...` packages remain in the complete Linux
  amd64 gate because their client tests are container-capable and have Unix
  host assumptions. Both native test jobs run uncached and preserve plain logs:
  the upstream command tests temporarily capture `os.Stdout` and `os.Stderr`,
  which makes Go's `-json` test stream report a false package failure even
  though the same uncached native tests pass without that formatter. The Linux
  job retains race detection and prepares both Docker- and LXC-dependent test
  capabilities before exercising the complete suite. Background Git
  maintenance is disabled while upstream temporary-repository assertions run,
  avoiding cleanup races with detached commit-graph writes. GitHub
  workflow-command parsing is suspended only while the upstream test transcript
  is streamed, so fixture output such as `##[add-matcher]` remains inert test
  data; the shell exit trap restores command processing for subsequent steps.

Each job produces its executable and deterministic SPDX 2.3 JSON SBOM. The
read-only finalizer assembles the exact asset inventory, writes `SHA256SUMS`
and `SOURCE-PROVENANCE.json`, verifies the bundle, and uploads it as a run
artifact. The separate manual release workflow consumes that exact qualified
bundle; only its publication job receives Release, OIDC, and attestation write
permissions.

After the automation PR is merged and current-head CI is terminal-successful,
perform the named backfill:

```sh
gh workflow run runner-release.yml \
  --repo creator-signal/fork-forgejo-runner \
  --ref creator-signal/automation \
  -f tag=v13.0.0 \
  -F publish=true
```

After that publishing run succeeds, dispatch
`runner-release-verification.yml` for the same tag. This separate read-only
workflow downloads the published bundle afresh on native Linux amd64, Linux
arm64, and Windows amd64 hosts; verifies the complete checksum/SBOM/source
inventory and both attestation predicates; and reruns version/configuration
smoke checks on the downloaded executable.

If upstream has advanced to a newer intended stable tag, update Issue #1 first
with the old and new tags and exact source SHAs. The named `v13.0.0` acceptance
case must still be explicitly reconciled; do not silently substitute a tag.

## Idempotency and interrupted drafts

A publishing run creates a draft record, attests the assembled bytes, uploads
only absent assets, compares any already-present draft asset byte-for-byte, and
publishes the draft only after the complete asset set verifies. It never uses
asset replacement.

If a run stops after draft creation, rerun the same tag. The workflow resumes
missing uploads and refuses a same-name asset with different bytes. Do not
delete the draft, tag, Release, or package to make a rerun pass. A non-draft
Release with an incomplete inventory fails closed and requires an Issue-recorded
repository-owner decision.

For an already complete Release, the workflow rebuilds all executables and
compares them to independently downloaded published bytes. It then validates
checksums, source metadata, provenance attestations, and SBOM attestations. It
does not upload or replace anything.

## Independent post-publication verification

Use a new empty directory outside the build checkout:

```sh
tag=v13.0.0
repo=creator-signal/fork-forgejo-runner
gh release download "$tag" --repo "$repo" --dir published-runner
cd published-runner
sha256sum --check SHA256SUMS
```

Verify both attestation predicates for every executable:

```sh
for binary in \
  forgejo-runner-13.0.0-linux-amd64 \
  forgejo-runner-13.0.0-linux-arm64 \
  forgejo-runner-13.0.0-windows-amd64.exe; do
  gh attestation verify "$binary" --repo "$repo" \
    --predicate-type https://slsa.dev/provenance/v1
  gh attestation verify "$binary" --repo "$repo" \
    --predicate-type https://spdx.dev/Document/v2.3
done
```

Run the Linux amd64 executable on an amd64 Linux host, Linux arm64 on a native
arm64 Linux host, and the Windows executable in native PowerShell. For each,
require exact `--version` output, generate a configuration, verify the `log`,
`runner`, and `container` sections, and require `daemon --help` to succeed. Do
not register the executable during release verification.

Finally verify that the Release source tag and `SOURCE-PROVENANCE.json` both
name the expected upstream SHA, the workflow run used the merged automation
revision, the protected default branch remains `creator-signal/automation`, the
scheduled workflow is active, and the owning Issue is closed only after all
evidence is recorded.
