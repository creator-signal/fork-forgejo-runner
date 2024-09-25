#!/usr/bin/env bash

set -e

# Technically not nessecary but it cleans up the logs from having token/secret values
run_command() {
  local cmd="$@"
  # Replace any --token <value> or --secret <value> with [REDACTED]
  local safe_cmd=$(echo "$cmd" | sed -E 's/--(token|secret) [^ ]+/--\1 [REDACTED]/g')
  decho "Running command: $safe_cmd"
  eval $cmd
}

decho() {
  if [[ "${DEBUG}" == "true" ]]; then
    echo "[entrypoint] $@"
  fi
}
decho $PWD

# Check if the script is running as root
if [[ $(id -u) -eq 0 ]]; then
  ISROOT=true
  decho "[WARNING] Running as root user"
fi

# Handle if `command` is passed, as command appends arguments to the entrypoint
if [ "$#" -gt 0 ]; then
    run_command $@
    exit
fi

# Handle and alter the config file
if [[ -z "${CONFIG_FILE}" ]]; then
  echo "CONFIG_FILE is not set"
  CONFIG_FILE="/data/config.yml"
fi
CONFIG_ARG="--config ${CONFIG_FILE}"
decho "CONFIG: ${CONFIG_ARG}"

DOCKER_HOST=${DOCKER_HOST:-"tcp://docker:2367"}
DOCKER_CERT_PATH=${DOCKER_CERT_PATH:-"/certs/client"}
DOCKER_TLS_VERIFY=${DOCKER_TLS_VERIFY:-1}
decho "DOCKER_HOST: ${DOCKER_HOST}"
decho "DOCKER_CERT_PATH: ${DOCKER_CERT_PATH}"
decho "DOCKER_TLS_VERIFY: ${DOCKER_TLS_VERIFY}"
if [[ ! -f "${CONFIG_FILE}" ]]; then
  echo "Creating ${CONFIG_FILE}"
  run_command "forgejo-runner generate-config > ${CONFIG_FILE}"

  # Remove test environment variables if they exist in the config file
  sed -i "/^    A_TEST_ENV_NAME_1:/d" ${CONFIG_FILE}
  sed -i "/^    A_TEST_ENV_NAME_2:/d" ${CONFIG_FILE}

  # Apply default values for docker
  sed -i "/^  labels:/c\  labels: [\"docker:docker://code.forgejo.org/oci/node:20-bookworm\", \"ubuntu-22.04:docker://catthehacker/ubuntu:act-22.04\"]" ${CONFIG_FILE}
  sed -i "/^  network:/c\  network: host" ${CONFIG_FILE}

  if [[ "${DOCKER_PRIVILEGED}" == "true" ]]; then
    sed -i "/^  privileged:/c\  privileged: true" ${CONFIG_FILE}
    sed -i "/^  options:/c\  options: -v /certs/client:/certs/client:ro" ${CONFIG_FILE}
    sed -i "/^  valid_volumes:/c\  valid_volumes:\n    - /certs/client" ${CONFIG_FILE}

    sed -i "/^  envs:/c\  envs:\n    DOCKER_HOST: ${DOCKER_HOST}\n    DOCKER_TLS_VERIFY: ${DOCKER_TLS_VERIFY}\n    DOCKER_CERT_PATH: ${DOCKER_CERT_PATH}" ${CONFIG_FILE}
  fi

fi

ENV_FILE=${ENV_FILE:-"/data/.env"}
decho "ENV_FILE: ${ENV_FILE}"
sed -i "/^  env_file:/c\  env_file: ${ENV_FILE}" ${CONFIG_FILE}

EXTRA_ARGS=""
if [[ ! -z "${RUNNER_LABELS}" ]]; then
  EXTRA_ARGS="${EXTRA_ARGS} --labels ${RUNNER_LABELS}"
fi
decho "EXTRA_ARGS: ${EXTRA_ARGS}"

# Set the runner file
RUNNER_FILE=${RUNNER_FILE:-"runner.json"} # use json so editors know how to highlight
decho "RUNNER_FILE: ${RUNNER_FILE}"
sed -i "/^  file:/c\  file: ${RUNNER_FILE}" ${CONFIG_FILE}

if [[ "${SKIP_WAIT}" != "true" ]]; then
  echo "Waiting 10s to allow other services to start up..."
  sleep 10
fi

if [[ ! -s "${RUNNER_FILE}" ]]; then
  touch ${RUNNER_FILE}
  try=$((try + 1))
  success=0
  decho "try: ${try}, success: ${success}"

  # The point of this loop is to make it simple, when running both forgejo-runner and gitea in docker,
  # for the forgejo-runner to wait a moment for gitea to become available before erroring out.  Within
  # the context of a single docker-compose, something similar could be done via healthchecks, but
  # this is more flexible.
  while [[ $success -eq 0 ]] && [[ $try -lt ${MAX_REG_ATTEMPTS:-10} ]]; do
    if [[ ! -z "${FORGEJO_SECRET}" ]]; then
      run_command forgejo-runner create-runner-file --connect \
    --instance "${FORGEJO_URL:-http://forgejo:3000}" \
    --name "${RUNNER_NAME:-$(hostname)}" \
    --secret "${FORGEJO_SECRET}" \
    ${CONFIG_ARG}\
    ${EXTRA_ARGS} 2>&1 | tee /tmp/reg.log
    else
      run_command forgejo-runner register \
    --instance "${FORGEJO_URL:-http://forgejo:3000}" \
    --name "${RUNNER_NAME:-$(hostname)}" \
    --token "${RUNNER_TOKEN}" \
    --no-interactive \
    ${CONFIG_ARG}\
    ${EXTRA_ARGS} 2>&1 | tee /tmp/reg.log
    fi
    cat /tmp/reg.log | grep -E 'connection successful|registered successfully' >/dev/null
    if [[ $? -eq 0 ]]; then
      echo "SUCCESS"
      success=1
    else
      echo "Waiting to retry ..."
      sleep 5
    fi
    decho "try: ${try}, success: ${success}"
  done
fi

# Prevent reading the token from the forgejo-runner process
unset RUNNER_TOKEN
unset FORGEJO_SECRET

run_command forgejo-runner daemon ${CONFIG_ARG}
