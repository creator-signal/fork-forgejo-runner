#!/bin/sh

if [ "$PATH" != "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" ] ; then
  echo "Container's PATH has been overridden: $PATH"
  exit 1
fi

if [ ! -d "/var/run/act" ] ; then
  echo "/var/run/act is missing"
  exit 1
fi

if [ ! -d "/var/run/act/workflow" ] ; then
  echo "/var/run/act/workflow is missing"
  exit 1
fi

if [ ! -f "actions/docker-hostexecutor/Dockerfile" ]; then
  echo "Dockerfile does not exist"
  exit 1
fi
