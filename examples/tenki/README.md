# Tenki sandbox backend

Run each job in a disposable [Tenki](https://tenki.cloud) microVM. A job is
routed to Tenki when its `runs-on` label uses the `tenki://` scheme. The sandbox
is created when the job starts and destroyed when it ends.

Only the sandbox's stable create/exec/destroy path is used — no volumes or
snapshots. Docker container actions (`uses: docker://…`) and service containers
are not supported inside the microVM.

## Configure

Add a `tenki` section to the runner `config.yml` and register a label with the
`tenki://` scheme:

```yaml
runner:
  labels:
    - "tenki:tenki://"          # optionally pin an image: "tenki:tenki://my-image"

tenki:
  # API key and project. Leave blank to read them from the environment
  # (TENKI_API_KEY / TENKI_PROJECT_ID) so the key is not written to disk.
  token: ""
  project_id: ""
  cpu: 2
  memory_mb: 4096
  disk_gb: 30
  max_lifetime: 3h              # Tenki reclaims the sandbox after this, guarding against leaks
```

Start the daemon with the credentials in the environment:

```bash
export TENKI_API_KEY=tk_...
export TENKI_PROJECT_ID=...
forgejo-runner --config config.yml daemon
```

## Use in a workflow

```yaml
on: [push]
jobs:
  test:
    runs-on: tenki
    steps:
      - run: echo "running in a Tenki microVM"
```

All jobs on the `tenki` label share the resources set in `config.yml`. Pin a
different image per label with `runs-on: <name>` where the label is declared as
`<name>:tenki://<image>`.
