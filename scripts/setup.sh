#!/bin/sh
set -e
set -o pipefail

function finish {
    echo $SD_STEP_ID $?
}
trap finish EXIT
