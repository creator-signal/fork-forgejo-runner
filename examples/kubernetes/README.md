# Kubernetes Docker in Docker Deployment

Registers Kubernetes Pod runners using [offline registration](https://forgejo.org/docs/latest/admin/runner-installation/#offline-registration), allowing the scaling of runners as needed.

NOTE: Docker in Docker (dind) requires elevated privileges on Kubernetes. The current way to achieve this is to set the pod `SecurityContext` to `privileged`. Keep in mind that this is a potential security issue that has the potential for a malicious application to break out of the container context.

[`dind-docker.yaml`](dind-docker.yaml) creates a Deployment and Secret for Kubernetes to act as a runner. The Docker credentials are re-generated each time the pod connects and does not need to be persisted.

Do not forget to update `FORGEJO_INSTANCE_URL` value.

# Build you first container image
First, you will need to generate an Applications Access token with the permission `write:package` (see [doc](https://forgejo.org/docs/latest/user/token-scope/)), usually from https://your-forgejo.fr/user/settings/applications.

Then, you will create 2 forgejo Actions [Secrets](https://forgejo.org/docs/latest/user/actions/#secrets):
  - `USERNAME_WRITE_REPOSITORY` containing Token name
  - `PASSWORD_WRITE_REPOSITORY` containing Token value

And you can then, use the [`build.yaml`](build.yaml) file provided as exemple.

This file must be created in your repository under: `.forgejo/workflows/build.yaml`
