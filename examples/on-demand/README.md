# On Demand Runners

This simplistic example demonstrates how to use the API of Forgejo 13 or newer to create Forgejo Runners on demand in response to waiting jobs. It can serve as a starting point for autoscaling runners.

Risks, limitations, and problems to be aware of:

* Dynamically created runners pile up. [Cleanup Offline Runners](https://forgejo.org/docs/v13.0/admin/config-cheat-sheet/#cron---cleanup-offline-runners-croncleanup_offline_runners) helps to a certain extent. However, it might remove persistent runners that have been offline for too long.
* Autoscaling usually involves using Forgejo Runner's `host` mode, which might enable running jobs to hijack runner registration tokens.
* Jobs may end up on any container or virtual machine with the same set of labels. This can lead to a multitude of problems, especially around cancellation, resource reclamation, and job isolation.

There might be more.

## Requirements

* Bash 4.3 or newer
* curl
* Docker
* [Forgejo Runner 11.3.0](https://code.forgejo.org/forgejo/runner/releases/tag/v11.3.0) or newer
* jq

All programs must be on `$PATH`.

## Run the Demo

*Caution*: While the workflows run in a container, it might still be a good idea to run the program in a virtual machine.

To start the demo, run:

```shell
$ ./run.sh <forgejo_url> <forgejo_user> <forgejo_password> [<max_jobs>]
```

For example, if Forgejo is reachable at http://localhost:3000 with the username `root` and the password `admin1234`, run:

```shell
./run.sh http://localhost:3000 root admin1234
```

The program will process jobs until you stop it with Ctrl+C. Alternatively, you can pass the maximum number of jobs to process as fourth argument:

```shell
./run.sh http://localhost:3000 root admin1234 5
```

The program will now stop after it has processed five jobs.

If you do not have a Forgejo instance ready for testing, you can create one with the following command:

```shell
$ docker run \
    --rm \
    --name forgejo \
    -p 3000:3000 \
    -e FORGEJO__server__LFS_START_SERVER=true \
    -e FORGEJO__security__INSTALL_LOCK=true \
    -e FORGEJO__log__LEVEL=debug \
    -e FORGEJO__server__ROOT_URL=http://0.0.0.0:3000/ \
    codeberg.org/forgejo/forgejo:13 \
    bash -c '/usr/bin/s6-svscan /etc/s6 & while : ; do su -c "forgejo admin user create --admin --username root --password admin1234 --email root@example.com" git && break ; echo -n . ; done && sleep infinity'
```

This Forgejo instance will be reachable at http://localhost:3000 with the username `root` and the password `admin1234`.