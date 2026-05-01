#!/bin/sh
set -eu
output="$(sh app.sh)"
test "$output" = "fixed"
echo "shell script verified"
