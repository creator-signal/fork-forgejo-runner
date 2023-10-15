# Forgejo Runner

**WARNING:** this is [alpha release quality](https://en.wikipedia.org/wiki/Software_release_life_cycle#Alpha) code and should not be considered secure enough to deploy in production.

A daemon that connects to a Forgejo instance and runs jobs for continous integration. The [installation and usage instructions](https://forgejo.org/docs/next/admin/actions/) are part of the Forgejo documentation.

# Hacking

The Forgejo runner depends on [a fork of ACT](https://code.forgejo.org/forgejo/act) and is a dependency of the [setup-forgejo action](https://code.forgejo.org/actions/setup-forgejo). Together they provide a development environment with end to end testing. Each repository also has some unit testing that can be used to quickly detect the simplest mistakes such as a failure to compile or static code checking failures (vulnerability, lint, etc.).

Assuming the modifications to the [Forgejo runner](https://code.forgejo.org/forgejo/runner) are pushed to a fork in a branch named `wip-runner-change`, a pull request will verify it compiles and the binary is sane (running `forgejo-runner --version`). It will not verify that it is able to properly run jobs when connected to a live Forgejo instance.

For end to end testing, a [workflow](https://code.forgejo.org/forgejo/runner/src/branch/main/.forgejo/workflows/cascade-setup-forgejo.yml) will create a [cascading-pr](https://code.forgejo.org/actions/cascading-pr/) in [setup-forgejo](https://code.forgejo.org/actions/setup-forgejo/pulls) and wait for its success.

The runner can be released by merging the `wip-runner-change` branch and by pushing a new tag, for instance `v10.2.3`. For more information see the [documentation that details this release process](https://forgejo.org/docs/next/developer/RELEASE/#forgejo-runner-publication) in the Forgejo infrastructure. Once published, the [setup-forgejo](https://code.forgejo.org/actions/setup-forgejo/) action can be updated to default to this latest version knowing it already passed integration tests.

## ACT

Assuming the modifications to [ACT](https://code.forgejo.org/forgejo/act) are pushed to a fork in a branch named `wip-act-change`, a pull request will verify it compiles. It will not verify that the Forgejo runner can compile with it.

For verifying it is compatible with the Forgejo runner, a branch should be pushed to a fork of the [Forgejo runner](https://code.forgejo.org/forgejo/runner) (for instance `wip-runner-change`) that uses the ACT version under test in `wip-act-change` by modifying `go.mod` to contain something like the following and running `go mod tidy`:

```
replace github.com/nektos/act => code.forgejo.org/earl-warren/act wip-act-change
```

Where https://code.forgejo.org/earl-warren/act is the URL of the ACT fork and `wip-act-change` is the branch where the changes under test were pushed. It will not verify that it is able to properly run jobs when connected to a live Forgejo instance. The `wip-runner-change` branch must, in turn, be tested as explained above. When the Forgejo runner modified to include the changes in the `wip-act-change` branch pass the end to end test of the `setup-forgejo` action, it is ready to be released.

ACT can be released by merging the `wip-act-change` branch and by pushing a new tag, for instance `v48.8.20`. Once published, the Forgejo runner can be updated to default to this latest version knowing it already passed end to end tests with something like:

```
replace github.com/nektos/act => code.forgejo.org/forgejo/act v48.8.20
```

## Local debug

The repositories are checked out in the same directory:

- **runner**: [Forgejo runner](https://code.forgejo.org/forgejo/runner)
- **act**: [ACT](https://code.forgejo.org/forgejo/act)
- **setup-forgejo**: [setup-forgejo](https://code.forgejo.org/actions/setup-forgejo)

### Install dependencies

The dependencies are installed manually or with:

```shell
setup-forgejo/forgejo-dependencies.sh
```

### Build the Forgejo runner with the local ACT

The Forgejo runner is rebuilt with the ACT directory by changing the `runner/go.mod` file to:

```
replace github.com/nektos/act => ../act
```

Running:

```
cd runner ; go mod tidy
```

Building:

```shell
cd runner ; rm -f forgejo-runner ; make forgejo-runner
```

### Launch Forgejo and the runner

A Forgejo instance is launched with:

```shell
cd setup-forgejo ; ./forgejo.sh setup
firefox http://$(cat forgejo-ip):3000
```

The user is `root` with password `admin1234`. The runner is registered with:

```
cd setup-forgejo
docker exec --user 1000 forgejo forgejo actions generate-runner-token > forgejo-runner-token
../runner/forgejo-runner register --no-interactive --instance "http://$(cat forgejo-ip):3000/" --name runner --token $(cat forgejo-runner-token) --labels docker:docker://node:16-bullseye,self-hosted
```

And launched with:

```shell
cd setup-forgejo ; ../runner/forgejo-runner --config runner-config.yml daemon
```

Note that the `runner-config.yml` is required in that particular case
to configure the network in `bridge` mode, otherwise the runner will
create a network that cannot reach the forgejo instance.

### Try a sample workflow

From the Forgejo web interface, create a repository and add the following to `.forgejo/workflows/try.yaml`. It will launch the job and the result can be observed from the `actions` tab.

```yaml
on: [push]
jobs:
  ls:
    runs-on: docker
    steps:
      - uses: actions/checkout@v3
      - run: |
          ls ${{ github.workspace }}
```
