#!/bin/sh
set -eu

if [ "${CI_XCODEBUILD_ACTION:-}" = "archive" ]; then
  echo "State archive completed successfully."
fi
