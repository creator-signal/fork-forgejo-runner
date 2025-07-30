# Forgejo Runner

**WARNING:** this is [alpha release quality](https://en.wikipedia.org/wiki/Software_release_life_cycle#Alpha) code and should not be considered secure enough to deploy in production.

A daemon that connects to a Forgejo instance and runs jobs for continuous integration. The [installation and usage instructions](https://forgejo.org/docs/next/admin/actions/) are part of the Forgejo documentation.

# Reporting bugs

When filing a bug in [the issue tracker](https://code.forgejo.org/forgejo/runner/issues), it is very helpful to propose a pull request [in the end-to-end tests](https://code.forgejo.org/forgejo/end-to-end/src/branch/main/actions) repository that adds a reproducer. It will fail the CI and unambiguously demonstrate that the problem exists. In most cases it is enough to add a workflow ([see the echo example](https://code.forgejo.org/forgejo/end-to-end/src/branch/main/actions/example-echo)). For more complicated cases it is also possible to add a runner config file as well as shell scripts to setup and teardown the test case ([see the service example](https://code.forgejo.org/forgejo/end-to-end/src/branch/main/actions/example-service)).

Sensitive security-related issues should be reported to [security@forgejo.org](mailto:security@forgejo.org) using [encryption](https://keyoxide.org/security@forgejo.org).

# Architectures & OS

The Forgejo runner is supported and tested on `amd64` and `arm64` ([binaries](https://code.forgejo.org/forgejo/runner/releases) and [containers](https://code.forgejo.org/forgejo/-/packages/container/runner/versions)) on Operating Systems based on the Linux kernel.

Work may be in progress for other architectures and you can browse the corresponding issues to figure out how they make progress. If you are interested in helping them move forward, open an issue. The most challenging part is to setup and maintain a native runner long term. Once it is supported by Forgejo, the runner is expected to be available 24/7 which can be challenging. Otherwise debugging any architecture specific problem won't be possible.

- [linux-s390x](https://code.forgejo.org/forgejo/runner/issues?labels=969)
- [linux-powerpc64le](https://code.forgejo.org/forgejo/runner/issues?labels=968)
- [linux-riscv64](https://code.forgejo.org/forgejo/runner/issues?labels=970)
- [Windows](https://code.forgejo.org/forgejo/runner/issues?labels=365)

# Hacking

The Forgejo runner is a dependency of the [setup-forgejo action](https://code.forgejo.org/actions/setup-forgejo). See [the full dependency graph](https://code.forgejo.org/actions/cascading-pr/#forgejo-dependencies) for a global view.

## Building

- Install [Go](https://go.dev/doc/install) and `make(1)`
- `make build`

The [test workflow](.forgejo/workflows/test.yml) is a full example that builds the binary, runs the tests and launches the runner binary against a live Forgejo instance.

## Generate mocks

- `make deps-tools`
- `make generate`

If there are changes, commit them to the repository.

## Local debug

The repositories are checked out in the same directory:

- **runner**: [Forgejo runner](https://code.forgejo.org/forgejo/runner)
- **setup-forgejo**: [setup-forgejo](https://code.forgejo.org/actions/setup-forgejo)

### Install dependencies

The dependencies are installed manually or with:

```shell
setup-forgejo/forgejo-dependencies.sh
```

### Build the Forgejo runner

```shell
cd runner ; rm -f forgejo-runner ; make forgejo-runner
```

### Launch Forgejo and the runner

A Forgejo instance is launched with:

```shell
cd setup-forgejo
./forgejo.sh setup
firefox $(cat forgejo-url)
```

The user is `root` with password `admin1234`. The runner is registered with:

```
cd setup-forgejo
docker exec --user 1000 forgejo forgejo actions generate-runner-token > forgejo-runner-token
../runner/forgejo-runner register --no-interactive --instance "$(cat forgejo-url)" --name runner --token $(cat forgejo-runner-token) --labels docker:docker://node:22-bookworm,self-hosted:host,lxc:lxc://debian:bookworm
```

And launched with:

```shell
cd setup-forgejo ; ../runner/forgejo-runner --config runner-config.yml daemon
```

Note that the `runner-config.yml` is required in that particular case
to configure the network in `bridge` mode, otherwise the runner will
create a network that cannot reach the forgejo instance.

### Try a sample workflow

From the Forgejo web interface, create a repository and add the
following to `.forgejo/workflows/try.yaml`. It will launch the job and
the result can be observed from the `actions` tab.

```yaml
on: [push]
jobs:
  ls:
    runs-on: docker
    steps:
      - uses: actions/checkout@v4
      - run: |
          ls ${{ github.workspace }}
```
