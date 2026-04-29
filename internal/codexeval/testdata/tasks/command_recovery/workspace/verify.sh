#!/bin/sh
set -eu

grep -qx 'status=ready' status.txt
echo 'status ready'
