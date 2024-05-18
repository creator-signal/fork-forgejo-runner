import json

expectedLabels = {
    "maintainer": "contact@forgejo.org",
    "org.opencontainers.image.authors": "Forgejo",
    "org.opencontainers.image.url": "https://forgejo.org",
    "org.opencontainers.image.documentation": "https://forgejo.org/docs/latest/admin/actions/#forgejo-runner",
    "org.opencontainers.image.source": "https://code.forgejo.org/forgejo/runner",
    "org.opencontainers.image.version": "1.2.3",
    "org.opencontainers.image.vendor": "Forgejo",
    "org.opencontainers.image.licenses": "MIT",
    "org.opencontainers.image.title": "Forgejo Runner",
    "org.opencontainers.image.description": "A runner for Forgejo Actions.",
}
inspect = None
with open("./labels.json", "r") as f:
    inspect = json.load(f)

assert inspect
labels = inspect[0]["Config"]["Labels"]

for k, v in expectedLabels.items():
    assert k in labels, f"'{k}' is missing from labels"
    assert labels[k] == v, f"expected {v} in key {k}, found {labels[k]}"
