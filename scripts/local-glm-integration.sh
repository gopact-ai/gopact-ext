#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

(cd models/glm && go test -tags=integration -count=1 ./...)
