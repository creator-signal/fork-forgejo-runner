#!/bin/sh
echo "$1"
echo "TEST_RESULT=$1" >> "$FORGEJO_ENV"
