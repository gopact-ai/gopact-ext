#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

(cd models/agnes && go test -tags=integration -count=1 ./...)
(cd tests/agents && go test -tags=integration -count=1 ./...)
