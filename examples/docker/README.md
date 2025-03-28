The following assumes:

* a docker server runs on the host
* the docker group of the host is GID 133
* a `.runner` file exists in /tmp/data
* a `runner-config.yml` file exists in /tmp/data

```sh
docker run -v /var/run/docker.sock:/var/run/docker.sock  -v /tmp/data:/data --user 1000:133 --rm code.forgejo.org/forgejo/runner:6.0.1 forgejo-runner --config runner-config.yaml daemon
```

The workflows will run using the host docker server
