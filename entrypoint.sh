#!/usr/bin/env bash

set -e

# Technically not necessary, but it cleans up the logs from having token/secret values
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

# Set default values (if needed)
RUNNER__runner__FILE="${RUNNER__runner__FILE:-/data/runner.json}"
RUNNER__CONFIG_FILE="${RUNNER__CONFIG_FILE:-/data/runner.yml}"

ENV_FILE="${ENV_FILE:-/data/.env}"
# Set config arguments
CONFIG_ARG="--config ${RUNNER__CONFIG_FILE}"
# Show config variables
decho "CONFIG: ${CONFIG_ARG}"

# Generate config if not found
if [[ ! -f "${RUNNER__CONFIG_FILE}" ]]; then
  echo "Creating ${RUNNER__CONFIG_FILE}"
  run_command "forgejo-runner generate-config > ${RUNNER__CONFIG_FILE}"
fi

# Use environment variables directly in the config, no need for sed edits
decho "Using config from: ${RUNNER__CONFIG_FILE}"
decho "Using environment file: ${ENV_FILE}"

# Set extra arguments from environment variables
EXTRA_ARGS=""
if [[ -n "${RUNNER__container__LABELS}" ]]; then
  EXTRA_ARGS="${EXTRA_ARGS} --labels ${RUNNER__container__LABELS}"
fi
decho "EXTRA_ARGS: ${EXTRA_ARGS}"

if [[ "${SKIP_WAIT}" != "true" ]]; then
  echo "Waiting 10s to allow other services to start up..."
  sleep 10
fi

FORGEJO_URL="${FORGEJO_URL:-http://forgejo:3000}"

# Try to register the runner
if [[ ! -s "${RUNNER__runner__FILE}" ]]; then
  touch ${RUNNER__runner__FILE}
  try=$((try + 1))
  success=0
  decho "try: ${try}, success: ${success}"

  while [[ $success -eq 0 ]] && [[ $try -lt ${MAX_REG_ATTEMPTS:-10} ]]; do
    if [[ -n "${FORGEJO_SECRET}" ]]; then
      run_command forgejo-runner create-runner-file --connect \
        --instance "${FORGEJO_URL}" \
        --name "${RUNNER__NAME:-$(hostname)}" \
        --secret "${FORGEJO_SECRET}" \
        ${CONFIG_ARG} \
        ${EXTRA_ARGS} 2>&1 | tee /tmp/reg.log
    else
      run_command forgejo-runner register \
        --instance "${FORGEJO_URL}" \
        --name "${RUNNER__NAME:-$(hostname)}" \
        --token "${RUNNER_TOKEN}" \
        --no-interactive \
        ${CONFIG_ARG} \
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
