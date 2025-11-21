A collection of Forgejo actions examples.

Other examples can be found [in the documentation](https://forgejo.org/docs/next/user/actions/).

| Section                     | Description                                                                                                                                                                                                                                                   |
|-----------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| [`docker-build-push-action-in-lxc`](../.forgejo/workflows/docker-build-push-action-in-lxc.yml)              | using the LXC runner backend to build and push a container image using the [docker/build-push-action](https://code.forgejo.org/docker/build-push-action) action                     |
| [`docker`](docker)              | using the host docker server by mounting the socket                     |
| [`LXC systemd`](lxc-systemd)              | systemd unit managing LXC containers dedicated to a single runner                     |
| [`docker-compose`](docker-compose) | all in one docker-compose with the Forgejo server, the runner and docker in docker  |
| [`kubernetes`](kubernetes)     |  a sample deployment for the Forgejo runner |
| [`on-demand`](on-demand)     |  create runners in response to pending Forgejo Action jobs |
