#! /usr/bin/env bash
# Copyright Forgejo Authors
# SPDX-License-Identifier: MIT

set -euo pipefail

if [ "$#" -lt 3 ]; then
    echo "Usage: $0 <forgejo_url> <forgejo_user> <forgejo_password> [<max_jobs>]"
    exit 1
fi

forgejo_url="$1"
forgejo_user="$2"
forgejo_password="$3"
max_jobs=""

if [[ "$#" == 4 ]]; then
    max_jobs="$4"
fi

# Declare available image labels and how to map them to container images.
declare -A images
images=(
    ["trixie"]="docker://debian:trixie"
    ["bookworm"]="docker://debian:bookworm"
    ["ubuntu-2404"]="docker://ubuntu:24.04"
)

# Declare additional labels that are *not* container images. Some autoscalers might require them
# for their own purposes, for example, routing or resource allocation (CPU, RAM).
IFS=" " read -r -a labels <<< "${!images[*]}"
labels+=("test")

# Create an API token with the name on-demand (can be any other name) for interacting with
# Forgejo's API. It requires read access to organizations and repositories. Normally, you would do
# this manually using the web UI.
token_response=$(curl -X POST \
    -H "Accept: application/json" \
    -H "Content-Type: application/json" \
    -d '{"name":"on-demand","scopes":["read:organization","read:repository"]}' \
    --basic \
    --user "$forgejo_user:$forgejo_password" \
    --no-progress-meter \
    "$forgejo_url/api/v1/users/$forgejo_user/tokens")

api_token=$(echo "$token_response" | jq -r '.sha1')

# Prepare a repository with the name on-demand and a scheduled workflow that runs every minute.
# See workflow.yaml in this repository for the workflow.
curl -X POST \
    -H "Accept: application/json" \
    -H "Content-Type: application/json" \
    -d '{"auto_init":true,"default_branch":"main","name":"on-demand","private":false}' \
    --basic \
    --user "$forgejo_user:$forgejo_password" \
    --no-progress-meter \
    "$forgejo_url/api/v1/user/repos"

workflow=$(base64 -w0 workflow.yaml)

curl -X POST \
    -H "Accept: application/json" \
    -H "Content-Type: application/json" \
    -d "{\"content\":\"$workflow\",\"message\":\"Create scheduled workflow\"}" \
    --basic \
    --user "$forgejo_user:$forgejo_password" \
    --no-progress-meter \
    "$forgejo_url/api/v1/repos/$forgejo_user/on-demand/contents/.forgejo%2Fworkflows%2Ftest.yaml"

# Get a runner registration token.
registration_token_response=$(curl -G \
    -H "Accept: application/json" \
    --basic \
    --user "$forgejo_user:$forgejo_password" \
    --no-progress-meter \
    "$forgejo_url/api/v1/repos/$forgejo_user/on-demand/actions/runners/registration-token")

registration_token=$(echo "$registration_token_response" | jq -r '.token')

joined_labels=$(IFS=, ; echo "${labels[*]}")

job_counter=0
while true; do
    echo "Polling Forgejo" >&2

    # Poll Forgejo for waiting jobs. Due to a limitation in Forgejo 13, we need to pass all
    # *possible* labels. Otherwise, zero jobs are returned. This restriction has been lifted in
    # Forgejo 14.
    jobs=$(curl -G \
        -H "Accept: application/json" \
        -H "Authorization: token $api_token" \
        --data-urlencode "labels=$joined_labels" \
        --no-progress-meter \
        "$forgejo_url/api/v1/repos/$forgejo_user/on-demand/actions/runners/jobs")

    if [[ "$jobs" == null* ]] ; then
        echo "No waiting jobs, sleeping for 5 seconds" >&2
        sleep 5

        continue
    fi

    waiting_jobs=$(echo "$jobs" | jq -r 'map(select(.status | contains("waiting"))) | length')

    # Loop over the waiting jobs and run them one after another. A real autoscaler would run them in
    # parallel.
    for (( i=0 ; i<"$waiting_jobs" ; i++ )); do
        # Generate a unique name for the runner. The runner will be used exactly once.
        runner_name=$(head /dev/urandom | tr -dc A-Za-z0-9 | head -c20)

        # Extract the labels of the waiting job. readyarray writes them to MAPFILE.
        readarray -t <<< "$(echo "$jobs" | jq -r --argjson job_index "$i" 'map(select(.status | contains("waiting"))) | .[$job_index].runs_on[]')"

        # Look in the array of labels of the waiting job for a label that maps to a container image.
        image_name=""
        for (( j = 0 ; j < "${#MAPFILE[*]}" ; j++ )); do
            if [[ -v images["${MAPFILE[$j]}"] ]]; then
                image_name=${images[${MAPFILE[$j]}]}
            fi
        done

        if [[ "$image_name" == "" ]] ; then
            echo "Skipping job because no image label present" >&2
            continue
        fi

        # Forgejo Runner expects labels in the format "label:container-image-to-load". If a job has
        # the labels [bookworm, test], the image to use is `docker://debian:bookworm` (see variable
        # `images` above). Therefore, the label string we have to generate is:
        # "bookworm:docker://debian:bookworm,test:docker://debian:bookworm"
        declare -a runner_labels=()
        for (( j = 0 ; j < "${#MAPFILE[*]}" ; j++ )); do
            runner_labels[j]="${MAPFILE[$j]}:$image_name"
        done

        # Run the job in a container. Normally, an autoscaler would provision a container or virtual
        # machine and start Forgejo Runner in there. The jobs would then be run in host mode. A job
        # with the labels [bookworm,test] would result in the label string "bookworm:host,test:host".
        forgejo-runner register --instance "$forgejo_url" --labels "$(IFS=, ; echo "${runner_labels[*]}")" --name "$runner_name" --token "$registration_token" --no-interactive
        forgejo-runner one-job

        ((job_counter+=1))
        if [[ "$max_jobs" == "$job_counter" ]] ; then
            echo "Maximum number of jobs processed" >&2
            exit 0
        fi
    done
done
