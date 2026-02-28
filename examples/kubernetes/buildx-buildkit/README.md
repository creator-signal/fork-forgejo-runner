Here is a schema of the build box:
```text
                         ┌─────────────────────────────────┐
                         │         Forgejo Server          │
                         │  https://my-beloved-forgejo.fr  │
                         └────-──────────┬─────────────────┘
                                         │ Jobs
                                         ▼
                   ┌────────────────────────────────────────────┐
                   │          Deployment: forgejo-runner        │
                   │                 (replicas: 2)              │
                   │                                            │
                   │  ┌──────────────────────────────────────┐  │
                   │  │ Pod forgejo-runner                   │  │
                   │  │                                      │  │
                   │  │  ┌───────────────────────────────┐   │  │
                   │  │  │ Container: runner             │   │  │
                   │  │  │ forgejo-runner daemon         │   │  │
                   │  │  │                               │   │  │
                   │  │  │ DOCKER_HOST=tcp://localhost   │   │  │
                   │  │  │ TLS client cert (/certs/...)  │   │  │
                   │  │  └───────────────┬───────────────┘   │  │
                   │  │                  │                   │  │
                   │  │                  ▼                   │  │
                   │  │  ┌───────────────────────────────┐   │  │
                   │  │  │ Container: docker:dind        │   │  │
                   │  │  │ TCP 2376 (TLS)                │   │  │
                   │  │  │ buildx client                 │   │  │
                   │  │  └───────────────┬───────────────┘   │  │
                   │  │                  │ TLS (ca.crt,      │  │
                   │  │                  │ tls.crt, tls.key) │  │
                   │  └──────────────────┼───────────────────┘  │
                   └─────────────────────┼──────────────────────┘
                                         │
                                         ▼
        ┌────────────────────────────────────────────────────────────┐
        │            StatefulSet: buildkitd (replicas: 2)            │
        │                                                            │
        │  ┌──────────────────────────────────────────────────────┐  │
        │  │ Pod buildkitd-0                                      │  │
        │  │                                                      │  │
        │  │  Container: moby/buildkit:v0.27.1-rootless           │  │
        │  │  TCP 1234 (TLS)                                      │  │
        │  │  --tlscacert /certs/ca.crt                           │  │
        │  │  --tlscert   /certs/tls.crt                          │  │
        │  │  --tlskey    /certs/tls.key                          │  │
        │  │  PVC: buildkitd-cache-buildkitd-0 (5Gi RWO)          │  │
        │  └──────────────────────────────────────────────────────┘  │
        │                                                            │
        │  ┌──────────────────────────────────────────────────────┐  │
        │  │ Pod buildkitd-1                                      │  │
        │  │  (same)                                              │  │
        │  │  PVC: buildkitd-cache-buildkitd-1 (5Gi RWO)          │  │
        │  └──────────────────────────────────────────────────────┘  │
        └────────────────────────────────────────────────────────────┘
```

# Deploy cert-manager
You will need [cert-manager](https://cert-manager.io/docs/installation/) to generate the certificates to authenticate buildx with buildkit.

# Create certificates
Inside the [certificate-chain](certificate-chain) folder, you will find 5 issuers and certificates.

Those certificates are the `cert-manager` way to replace the example provided in the official BuildKit repository [create-certs.sh](https://github.com/moby/buildkit/blob/master/examples/kubernetes/create-certs.sh)

Respect the order when applying:
- [01-issuer-self-signed.yaml](certificate-chain/01-issuer-self-signed.yaml)
- [02-certificate-ca.yaml](certificate-chain/02-certificate-ca.yaml)
- [03-issuer-ca.yaml](certificate-chain/03-issuer-ca.yaml)
- [04-certificate-daemon.yaml](certificate-chain/04-certificate-daemon.yaml)
- [05-certificate-client.yaml](certificate-chain/05-certificate-client.yaml)

After applying each manifest using `kubectl -n forgejo apply -f your-manifest.yaml`, wait until the corresponding resource reaches the Ready state before proceeding to the next one.

You can verify the status with (be sure to see READY set to True):
```bash
me@localhost:~/forgejo> kubectl -n forgejo apply -f 01-issuer-self-signed.yaml
me@localhost:~/forgejo> kubectl -n forgejo get issuer
NAME                   READY   AGE
buildkit-self-signed   True    28h

me@localhost:~/forgejo> kubectl -n forgejo apply -f 02-certificate-ca.yaml
me@localhost:~/forgejo> kubectl -n forgejo get certificate
NAME              READY   SECRET                 AGE
buildkit-ca       True    buildkit-ca-cert       28h

...
Repeat for each issuer and certificate
```

You can validate that all certificates are correctly issued and chained by comparing their CA content.

Run the following commands:
```bash
kubectl -n forgejo get secret buildkit-ca-cert -o jsonpath='{.data.ca\.crt}' | base64 --decode

kubectl -n forgejo get secret buildkit-daemon-cert -o jsonpath='{.data.ca\.crt}' | base64 --decode
kubectl -n forgejo get secret buildkit-client-cert -o jsonpath='{.data.ca\.crt}' | base64 --decode
```
Each command prints the CA certificate used by:
- the CA itself
- the buildkit daemon
- the buildkit client

All three outputs must be strictly identical.

If they differ, it means the daemon or client certificate is not signed by the expected CA, and TLS authentication between buildx and buildkit will fail.

Delete the resources and recreate them, making sure to wait for each manifest to be fully applied and reach the Ready state before proceeding to the next one.

Applying them too quickly may cause dependency issues, especially with certificates and issuers.

Proceed step by step, and verify readiness at each stage before continuing.

# Deploy buildkit daemon
Inside the [buildkit](buildkit) folder, you will find a StatefulSet and a Deployment.

We are using the official rootless examples from the BuildKit repository as a base:
- [StatefulSet](https://github.com/moby/buildkit/blob/master/examples/kubernetes/statefulset.rootless.yaml)
- [Deployment](https://github.com/moby/buildkit/blob/master/examples/kubernetes/deployment%2Bservice.rootless.yaml)

I chose the rootless StatefulSet variant because it is easier to scale horizontally if needed.

Each replica gets its own persistent cache volume, which makes scaling predictable and clean.

Just apply the [statefulset.yaml](buildkit/statefulset.yaml) from [buildkit](buildkit) folder (The persistent volume claim is configured with [5Gi](buildkit/statefulset.yaml#L126) of storage):
```bash
kubectl -n forgejo apply -f statefulset.yaml
```

If you prefer a simpler setup, you can use the [deployment.yaml](buildkit/deployment.yaml) from [buildkit](buildkit) folder.

Additionally, [readOnlyRootFilesystem](buildkit/statefulset.yaml#L86) is enabled for better security.

This is why several [emptyDir](buildkit/statefulset.yaml#L110-L117) volumes are defined: they provide writable locations for paths that BuildKit needs at runtime, since the container filesystem itself is read-only.

# Deploy forgejo runner with docker in docker
Inside the [runner](runner) folder, you will find all the required resources to prepare the Forgejo runner and configure Docker Buildx.

The setup itself is a fairly standard deployment combining a Forgejo runner with Docker-in-Docker.

The only non-trivial part is the TLS configuration: the BuildKit client certificates must be properly mounted into the runner and Docker containers to allow secure communication with the BuildKit daemon.

The [initContainers](runner/deployment.yaml#L79-L81) from [deployment.yaml](runner/deployment.yaml) also contain configuration to mount the generated buildkit client certificate to authenticate with the buildkit daemon.

As long as you keep the resource names consistent and follow the provided structure, everything should work as expected.

Registers Kubernetes Pod runners using [offline registration](https://forgejo.org/docs/latest/admin/runner-installation/#offline-registration), allowing the scaling of runners as needed.

NOTE: Docker in Docker (dind) requires privileged mode on Kubernetes. The current way to achieve this is to set the pod `SecurityContext` to `privileged`. Keep in mind that this is a potential security issue that has the potential for a malicious application to break out of the container context.

[deployment.yaml](runner/deployment.yaml) creates a Deployment and Secret for Kubernetes to act as a runner. The Docker credentials are re-generated each time the pod connects and does not need to be persisted.

Do not forget to update [FORGEJO_INSTANCE_URL](runner/deployment.yaml#L97) value.

Also, you will need to enter the information to connect to your forgejo registry in the [Secret](runner/deployment.yaml#L24).

Just deploy from the [deployment.yaml](runner/deployment.yaml) from the [runner](runner) folder:
```bash
kubectl -n forgejo apply -f deployment.yaml
```

# Your first workflow
Inside the [runner](runner) folder, you can use the file [your-first-build.yaml](runner/your-first-build.yaml) to start to build your first container.

First, you will need to generate an Applications Access token with the permission `write:package` (see [doc](https://forgejo.org/docs/latest/user/token-scope/)), usually from https://my-beloved-forgejo.fr/user/settings/applications.

Then, you will create 2 forgejo Actions [Secrets](https://forgejo.org/docs/latest/user/actions/#secrets):
  - `USERNAME_WRITE_REPOSITORY` containing Token name
  - `PASSWORD_WRITE_REPOSITORY` containing Token value

This file must be copied in your repository under: `.forgejo/workflows/build.yaml`

# Your second workflow
This [your-first-build.yaml](runner/your-first-build.yaml) from [runner](runner) folder could be improved, as the step [Docker and Buildx installation](runner/your-first-build.yaml#L26) could be done once and for all in a prepared container image.

So, use your [your-first-build.yaml](runner/your-first-build.yaml) to build the [Dockerfile](runner/builder/Dockerfile) inside the [runner/builder](runner/builder) folder.

This will produce your custom builder image, for example: `my-beloved-forgejo.fr/forgejo/builder:24-alpine-xxxxxxxx`.

Then, to be able to use it, you will need to update the forgejo-runner [deployment.yaml](runner/deployment.yaml).

Replace this [part](runner/deployment.yaml#L68-L77) of the [deployment.yaml](runner/deployment.yaml):
```yaml
          args:
            - |
              while : ; do
                forgejo-runner register \
                  --no-interactive \
                  --token "${RUNNER_SECRET}" \
                  --name "${RUNNER_NAME}" \
                  --instance "${FORGEJO_INSTANCE_URL}" && break ;
                sleep 1 ;
              done ;
```
with the following
```yaml
          args:
            - |
              while : ; do
                forgejo-runner register \
                  --no-interactive \
                  --token "${RUNNER_SECRET}" \
                  --name "${RUNNER_NAME}" \
                  --instance "${FORGEJO_INSTANCE_URL}" \
                  --labels 'docker:docker://my-beloved-forgejo.fr/forgejo/builder:24-alpine-xxxxxxxx' && break ;
                sleep 1 ;
              done ;
```
The `--labels` flag tells Forgejo to use your custom builder image for Docker jobs (format is `docker:docker://<url-of-the-image>`).

Once applied, all future builds executed by this runner will use your new image.

You can now use [your-second-build.yaml](runner/your-second-build.yaml) instead of [your-first-build.yaml](runner/your-first-build.yaml).

You runner will be able to pull your built image thanks to the Secret added [here](runner/deployment.yaml#L24) and mounted [here](runner/deployment.yaml#L156).
