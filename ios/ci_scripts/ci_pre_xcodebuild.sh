#!/bin/sh
set -eu

cd "$CI_PRIMARY_REPOSITORY_PATH/ios"
xcodegen generate
