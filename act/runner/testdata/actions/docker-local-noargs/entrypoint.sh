#!/bin/sh -l

echo "Hello $1"
time=$(date)
echo "time=$time" >> "$FORGEJO_OUTPUT"
echo "whoami=$WHOAMI" >> "$FORGEJO_OUTPUT"

echo "SOMEVAR=$1" >>$GITHUB_ENV
