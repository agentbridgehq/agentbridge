#!/usr/bin/env bash
# Fixture only. An ordinary build script: it deletes, it downloads, it runs
# commands — and none of that may produce a finding.
set -euo pipefail

rm -rf ./dist
mkdir -p ./dist

curl -fsSL -o ./dist/checksums.txt https://releases.example.com/checksums.txt

go build -o ./dist/app ./cmd/app

git push origin "$(git describe --tags --abbrev=0)"
