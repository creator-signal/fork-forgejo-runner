# Forgejo Runner

A daemon that connects to a Forgejo instance and runs jobs for continuous integration. The [installation and usage instructions](https://forgejo.org/docs/next/admin/actions/) are part of the Forgejo documentation. Forgejo Runner can also run workflows locally and act as a cache.

Forgejo Runner is distributed under the terms of the [GPL version 3.0](LICENSE) or any later version.

## Issues

* [Feature requests](https://code.forgejo.org/forgejo/forgejo-actions-feature-requests)
* [Runner-specific issues](https://code.forgejo.org/forgejo/runner/issues)
* [Forgejo Actions issues](https://codeberg.org/forgejo/forgejo/issues?q=&type=all&labels=96148&state=open)

It is totally okay to report issues in the "wrong" place; identifying the right place is hard. In the worst case, we will ask you to open it somewhere else because issues cannot be transferred between instances.

### Reporting Security Vulnerabilities

Please report security-related issues to [security@forgejo.org](mailto:security@forgejo.org) using [encryption](https://keyoxide.org/security@forgejo.org).

## Contributing

See the [contribution guide](CONTRIBUTING.md).

## Architectures & OS

The Forgejo runner is supported and tested on `amd64` and `arm64` ([binaries](https://code.forgejo.org/forgejo/runner/releases) and [containers](https://code.forgejo.org/forgejo/-/packages/container/runner/versions)) on Operating Systems based on the Linux kernel.

Work may be in progress for other architectures and you can browse the corresponding issues to figure out how they make progress. If you are interested in helping them move forward, open an issue. The most challenging part is to setup and maintain a native runner long term. Once it is supported by Forgejo, the runner is expected to be available 24/7 which can be challenging. Otherwise debugging any architecture specific problem won't be possible.

- [linux-s390x](https://code.forgejo.org/forgejo/runner/issues?labels=969)
- [linux-powerpc64le](https://code.forgejo.org/forgejo/runner/issues?labels=968)
- [linux-riscv64](https://code.forgejo.org/forgejo/runner/issues?labels=970)
- [Windows](https://code.forgejo.org/forgejo/runner/issues?labels=365)

## Hacking

The Forgejo runner is a dependency of the [setup-forgejo action](https://code.forgejo.org/actions/setup-forgejo). See [the full dependency graph](https://code.forgejo.org/actions/cascading-pr/#forgejo-dependencies) for a global view.

### Building

- Install [Go](https://go.dev/doc/install) and `make(1)`
- `make build`

### Linting

- `make lint-check`
- `make lint` # will fix some lint errors

### Testing

The [workflow](.forgejo/workflows/test.yml) that runs in the CI uses similar commands.

#### Without a Forgejo instance

- Install [Docker](https://docs.docker.com/engine/install/)
- `make test integration-test`

The `TestRunner_RunEvent` test suite contains most integration tests
with real-world workflows and is time-consuming to run. During
development, it is helpful to run a specific test through a targeted
command such as this:

- `go test -count=1 -run='TestRunner_RunEvent$/local-action-dockerfile$' ./act/runner`

#### With a Forgejo instance

- Run a Forgejo instance locally (for instance at http://0.0.0.0:8080) and create as shared secret
```sh
export FORGEJO_RUNNER_SECRET='AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'
export FORGEJO_URL=http://0.0.0.0:8080
forgejo forgejo-cli actions register --labels docker --name therunner --secret $FORGEJO_RUNNER_SECRET
```
- `make test integration-test` # which will run addional tests because FORGEJO_URL is set

#### end-to-end

- Follow the instructions from the end-to-end tests to [run actions tests locally](https://code.forgejo.org/forgejo/end-to-end#running-from-locally-built-binary).
- `./end-to-end.sh actions_teardown` # stop the Forgejo and runner daemons running in the end-to-end environment
- `( cd ~/clone-of-the-runner-repo ; make build ; cp forgejo-runner /tmp/forgejo-end-to-end/forgejo-runner )` # install the runner built from sources
- `./end-to-end.sh actions_setup 13.0` # start Forgejo v13.0 and the runner daemon in the end-to-end environment
- `./end-to-end.sh actions_verify_example echo` # run the [echo workflow](https://code.forgejo.org/forgejo/end-to-end/src/branch/main/actions/example-echo/.forgejo/workflows/test.yml)
- `xdg-open http://127.0.0.1:3000/root/example-echo/actions/runs/1` # see the logs workflow
- `less /tmp/forgejo-end-to-end/forgejo-runner.log` # analyze the runner logs
- `less /tmp/forgejo-end-to-end/forgejo-work-path/log/forgejo.log` # analyze the Forgejo logs
