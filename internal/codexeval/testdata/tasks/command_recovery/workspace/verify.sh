#!/bin/sh
set -eu

actual="$(cat status.txt)"
if [ "$actual" != "status=ready" ]; then
  echo "status.txt must contain exactly status=ready" >&2
  exit 1
fi
echo 'status ready'
