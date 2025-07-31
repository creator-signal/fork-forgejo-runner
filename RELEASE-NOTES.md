# Release Notes

As of [Forgejo runner v9.0.0](https://code.forgejo.org/forgejo/runner/releases/tag/v9.0.0) the release notes are published in the release itself and this file will no longer be updated.

## 8.0.1

* [tolerate strings for fail-fast, max-parallel, timeout-minutes, cancel-timeout-minutes](https://code.forgejo.org/forgejo/act/pulls/203).

## 8.0.0

* Breaking change: workflows files go through a [schema validation](https://code.forgejo.org/forgejo/act/pulls/170) and will not run if they do not pass. Some existing workflows may have syntax errors that did not prevent them from running with versions 7.0.0 and below but they will no longer work with versions 8.0.0 and above.
  Existing workflows can be verified and fixed before upgrading by using `forgejo-runner exec --workflows path-to-the-workflow`. For instance in a workflow where `ruins-on` was typed by mistake instead of `runs-on`:
  ```sh
  $ forgejo-runner exec --event unknown --workflows ../forgejo/.forgejo/workflows/build-release.yml
  Error: workflow is not valid. 'build-release.yml': Line: 32 Column 5: Failed to match job-factory: Line: 32 Column 5: Unknown Property ruins-on
  Line: 32 Column 5: Failed to match workflow-job: Line: 32 Column 5: Unknown Property ruins-on
  Line: 35 Column 5: Unknown Property steps
  Forgejo Actions YAML Schema validation error
  ```
  If the error is not immediately obvious, please file an issue with a copy of the failed workflow and revert to using version 7.0.0 until it is resolved.
* Breaking change: the logic assigning labels was updated and refactored:
  - in the absence of a label or a label, [default to `docker://node:22-bookworm` instead of `docker://node:20-bullseye` or `host`](https://code.forgejo.org/forgejo/runner/issues/134).
  - if the `lxc` scheme is set with no argument, it defaults to `lxc://debian:bookworm` instead of `lxc://debian:bullseye`.
  - the `host` schema cannot have any argument, it can no longer be `host://-self-hosted`
* Breaking change: [bash fallback to sh if it is not available](https://code.forgejo.org/forgejo/runner/issues/150). It will use `bash` instead of `sh` when a container image is explicitly specified in the step. If a workflow depens on that behavior, it will need to be modified to explictly set the shell to `sh`.
* Breaking change: [sanitize network aliases to be valid DNS names](https://code.forgejo.org/forgejo/act/pulls/190). It is breaking for workflows with services that rely on host names (derived from the service name or the job name) that do not match `[^A-Z0-9-]+`. They will be sanitized and a message displayed in the logs showing the sanitized name. The service can either be renamed to match the constraint so it can be used as is. Or the sanitized name can be used. For instance of a PostgreSQL service runs as `data.base` it will be sanitized as `data_base`.
* [secrets that contain multiple lines are masked from the output](https://code.forgejo.org/forgejo/runner/pulls/661).
* [sum256 the container name so derivations do not overflow](https://code.forgejo.org/forgejo/act/pulls/191).

## 7.0.0

* Breaking change: [forgejo-runner exec --forgejo-instance replaces --gitea-instance](https://code.forgejo.org/forgejo/runner/pulls/652).
* Breaking change: [forge.FORGEJO_* can be used instead of github.GITHUB_*](https://code.forgejo.org/forgejo/act/pulls/171), e.g. `forge.FORGEJO_REPOSITORY` is the same as `github.GITHUB_REPOSITORY`. The `GITHUB_*` environment variables are preserved indefinitely for backward compatibiliy with existing workflows and actions. A workflow that previously set preset `FORGEJO_*` variables in any context, they will be overridden by this naming change. For instance if `secrets.FORGEJO_TOKEN` was set, it will be set to the automatic token and instead of the value from the secrets of the repository. The same is true for `forge.FORGEJO_REPOSITORY` etc.
* [fix a v6.4.0 regression that fail a job when if: false](https://code.forgejo.org/forgejo/runner/issues/660).
* [support for forgejo-runner exec --var](https://code.forgejo.org/forgejo/runner/pulls/645).
* [do not force WORKING_DIR in service containers](https://code.forgejo.org/forgejo/runner/issues/304).
* [remove the local action cache if the remote has changed](https://code.forgejo.org/forgejo/act/pulls/142), e.g. when [DEFAULT_ACTIONS_URL](https://forgejo.org/docs/next/admin/config-cheat-sheet/#actions-actions) is modified in the forgejo configuration.

## 6.4.0

**Do not use, it [contains a regression](https://code.forgejo.org/forgejo/runner/issues/660) fixed in 7.0.0.**

* [Update code.forgejo.org/forgejo/act](https://code.forgejo.org/forgejo/runner/pulls/571) to v1.26.0. This brings [several security updates](https://code.forgejo.org/forgejo/act/compare/v1.25.1...v1.26.0), as well as [offline action caching](https://code.forgejo.org/forgejo/act/commit/613090ecd71f75e6200ded4c9d5424b26a792755).
* [Remove unused x-runner-version header](https://code.forgejo.org/forgejo/runner/pulls/496).
* [Upgrade lxc-systemd using a URL instead of a version](https://code.forgejo.org/forgejo/runner/pulls/520).
* [Correctly use HTTP proxy if insecure is true](https://code.forgejo.org/forgejo/runner/pulls/535).
* [Update golang.org/x/crypto](https://code.forgejo.org/forgejo/runner/pulls/562) to a version that is not susceptible to DOS attack.
* [Update golang.org/x/net](https://code.forgejo.org/forgejo/runner/pulls/563) to a version with several security fixes.

## 6.3.1

* [Fixed an issue which caused data races and timeouts](https://code.forgejo.org/forgejo/act/pulls/109) in certain cases, which would [cause cache storing and retrieval to fail](https://code.forgejo.org/forgejo/runner/issues/509).

## 6.3.0

* [Caches are now correctly scoped to repositories](https://code.forgejo.org/forgejo/runner/pulls/503). Require authentication for cache requests, and set up cache proxy to provide authentication transparently and automatically.

## 6.2.2

* LXC systemd service unit example script [learned how to upgrade](https://code.forgejo.org/forgejo/runner/pulls/475).

## 6.2.1

* LXC [templates are updated if needed](https://code.forgejo.org/forgejo/act/pulls/102).

## 6.2.0

* The `container.options` [allows `--hostname`](https://forgejo.org/docs/next/user/actions/#jobsjob_idcontaineroptions).

## 6.1.0

* [Add `[container].force_rebuild` config option](https://code.forgejo.org/forgejo/runner/pulls/406) to force rebuilding of local docker images, even if they are already present.
* [Add new `--one-job` flag](https://code.forgejo.org/forgejo/runner/pulls/423) to execute a previously configured runner, execute one task if it exists and exit. Motivation [here](https://code.forgejo.org/forgejo/runner/issues/422)

## 6.0.1

* [Fixes a regression](https://code.forgejo.org/forgejo/runner/issues/425) that was introduced in version 6.0.0 by which the `[container].options` config file setting was ignored.

## 6.0.0

* Security: the container options a job is allowed to specify are limited to a [predefined allow list](https://forgejo.org/docs/next/user/actions/#jobsjob_idcontaineroptions).

## 5.0.4

* Define FORGEJO_TOKEN as an alias to GITHUB_TOKEN

## 5.0.3

* [Fixes a regression](https://code.forgejo.org/forgejo/runner/pulls/354) that was introduced in version 5.0.0 by which it was no longer possible to mount the docker socket in each container by specifying `[container].docker_host = ""`. This is now implemented when `[container].docker_host = "automount"` is specified.

## 5.0.2

* Fixes a regression that was introduced in version 5.0.0 by which [skipped jobs were marked as failed instead](https://code.forgejo.org/forgejo/act/pulls/67). The workaround is to change the job log level to debug `[log].job_level: debug`.

## 5.0.1

* Security: the `/opt/hostedtoolcache` directory is now unique to each job instead of being shared to avoid a risk of corruption. It is still advertised in the `RUNNER_TOOL_CACHE` environment variable. Custom container images can be built to pre-populate this directory with frequently used tools and some actions (such as `setup-go`) will benefit from that.

## 5.0.0

* Breaking change: the default configuration for `docker_host` is changed to [not mounting the docker server socket](https://code.forgejo.org/forgejo/runner/pulls/305) even when no configuration file is provided.
* [Add job_level logging option to config](https://code.forgejo.org/forgejo/runner/pulls/299) to make the logging level of jobs configurable. Change default from "trace" to "info".
* [Don't log job output when debug logging is not enabled](https://code.forgejo.org/forgejo/runner/pulls/303). This reduces the default amount of log output of the runner.

## 4.0.1

* Do not panic when [the number of arguments of a function evaluated in an expression is incorect](https://code.forgejo.org/forgejo/act/pulls/59/files).

## 4.0.0

* Breaking change: fix the default configuration for `docker_host` is changed to [not mounting the docker server socket](https://code.forgejo.org/forgejo/runner/pulls/305).
* [Remove debug information from the setup of a workflow](https://code.forgejo.org/forgejo/runner/pulls/297).
* Fix [crash in some cases when the YAML structure is not as expected](https://code.forgejo.org/forgejo/runner/issues/267).

## 3.5.1

* Fix [CVE-2024-24557](https://nvd.nist.gov/vuln/detail/CVE-2024-24557)
* [Add report_interval option to config](https://code.forgejo.org/forgejo/runner/pulls/220) to allow setting the interval of status and log reports

## 3.5.0

* [Allow graceful shutdowns](https://code.forgejo.org/forgejo/runner/pulls/202): when receiving a signal (INT or TERM) wait for running jobs to complete (up to shutdown_timeout).
* [Fix label declaration](https://code.forgejo.org/forgejo/runner/pulls/176): Runner in daemon mode now takes labels found in config.yml into account when declaration was successful.
* [Fix the docker compose example](https://code.forgejo.org/forgejo/runner/pulls/175) to workaround the race on labels.
* [Fix the kubernetes dind example](https://code.forgejo.org/forgejo/runner/pulls/169).
* [Rewrite ::group:: and ::endgroup:: commands like github](https://code.forgejo.org/forgejo/runner/pulls/183).
* [Added opencontainers labels to the image](https://code.forgejo.org/forgejo/runner/pulls/195)
* [Upgrade the default container to node:20](https://code.forgejo.org/forgejo/runner/pulls/203)

## 3.4.1

* Fixes a regression introduced in 3.4.0 by which a job with no image explicitly set would
  [be bound to the host](https://code.forgejo.org/forgejo/runner/issues/165)
  network instead of a custom network (empty string in the configuration file).

## 3.4.0

Although this version is able to run [actions/upload-artifact@v4](https://code.forgejo.org/actions/upload-artifact/src/tag/v4) and [actions/download-artifact@v4](https://code.forgejo.org/actions/download-artifact/src/tag/v4), these actions will fail because it does not run against GitHub.com. A fork of those two actions with this check disabled is made available at:

* https://code.forgejo.org/forgejo/upload-artifact/src/tag/v4
* https://code.forgejo.org/forgejo/download-artifact/src/tag/v4

and they can be used as shown in [an example from the end-to-end test suite](https://code.forgejo.org/forgejo/end-to-end/src/branch/main/actions/example-artifacts-v4/.forgejo/workflows/test.yml).

* When running against codeberg.org, the default poll frequency is 30s instead of 2s.
* Fix compatibility issue with actions/{upload,download}-artifact@v4.
* Upgrade ACT v1.20.0 which brings:
  * `[container].options` from the config file is exposed in containers created by the workflows
  * the expressions in the value of `jobs.<job-id>.runs-on` are evaluated
  * fix a bug causing the evaluated expression of `jobs.<job-id>.runs-on` to fail if it was an array
  * mount `act-toolcache:/opt/hostedtoolcache` instead of `act-toolcache:/toolcache`
  * a few improvements to the readability of the error messages displayed in the logs
  * `amd64` can be used instead of `x86_64` and `arm64` intead of `aarch64` when specifying the architecture
  * fixed YAML parsing bugs preventing dispatch workflows to be parsed correctly
  * add support for `runs-on.labels` which is equivalent to `runs-on` followed by a list of labels
  * the expressions in the service `ports` and `volumes` values are evaluated
  * network aliases are only supported when the network is user specified, not when it is provided by the runner
* If `[runner].insecure` is true in the configuration, insecure cloning actions is allowed

## 3.3.0

* Support IPv6 with addresses from a private range and NAT for
    docker:// with --enable-ipv6 and [container].enable_ipv6
    lxc:// always

## 3.2.0

* Support LXC container capabilities via `lxc:lxc://debian:bookworm:k8s` or  `lxc:lxc://debian:bookworm:docker lxc k8s`
* Update ACT v1.16.0 to resolve a [race condition when bootstraping LXC templates](https://code.forgejo.org/forgejo/act/pulls/23)

## 3.1.0

The `self-hosted` label that was hardwired to be a LXC container
running `debian:bullseye` was reworked and documented ([user guide](https://forgejo.org/docs/next/user/actions/#jobsjob_idruns-on) and [admin guide](https://forgejo.org/docs/next/admin/actions/#labels-and-runs-on)).

There now are two different schemes: `lxc://` for LXC containers and
`host://` for running directly on the host.

* Support the `host://` scheme for running directly on the host.
* Support the `lxc://` scheme in labels
* Update [code.forgejo.org/forgejo/act v1.14.0](https://code.forgejo.org/forgejo/act/pulls/19) to implement both self-hosted and LXC schemes

## 3.0.3

* Update [code.forgejo.org/forgejo/act v1.13.0](https://code.forgejo.org/forgejo/runner/pulls/106) to keep up with github.com/nektos/act

## 3.0.2

* Update [code.forgejo.org/forgejo/act v1.12.0](https://code.forgejo.org/forgejo/runner/pulls/106) to upgrade the node installed in the LXC container to node20

## 3.0.1

* Update [code.forgejo.org/forgejo/act v1.11.0](https://code.forgejo.org/forgejo/runner/pulls/86) to resolve a bug preventing actions based on node20 from running, such as [checkout@v4](https://code.forgejo.org/actions/checkout/src/tag/v4).

## 3.0.0

* Publish a rootless OCI image
* Refactor the release process

## 2.5.0

* Update [code.forgejo.org/forgejo/act v1.10.0](https://code.forgejo.org/forgejo/runner/pulls/71)

## 2.4.0

* Update [code.forgejo.org/forgejo/act v1.9.0](https://code.forgejo.org/forgejo/runner/pulls/64)

## 2.3.0

* Add support for [offline registration](https://forgejo.org/docs/next/admin/actions/#offline-registration).
