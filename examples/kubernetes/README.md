## Kubernetes Docker in Docker Deployment with `act_runner`

Registers Kubernetes pod runners using [offline registration](https://forgejo.org/docs/v1.21/admin/actions/#offline-registration), allowing the scaling of runners as needed.

NOTE: Docker in Docker (dind) requires elevated privileges on Kubernetes. The current way to achieve this is to set the pod `SecurityContext` to `privileged`. Keep in mind that this is a potential security issue that has the potential for a malicious application to break out of the container context.

Files in this directory:

- [`dind-docker.yaml`](dind-docker.yaml)
  How to create a Deployment and Secret for Kubernetes to act as a runner. The Docker credentials are re-generated each time the pod connects and does not need to be persisted.

- [`rootless-docker.yaml`](rootless-docker.yaml)
  How to create a rootless Deployment and Secret for Kubernetes to act as a runner. The Docker credentials are re-generated each time the pod connects and does not need to be persisted.
