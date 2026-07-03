#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

(cd agents/agentnode && go test -count=1 ./...)
(cd agents/agenttool && go test -count=1 ./...)
(cd agents/humanreview && go test -count=1 ./...)
(cd agents/planexec && go test -count=1 ./...)
(cd agents/react && go test -count=1 ./...)
(cd agents/scheduler && go test -count=1 ./...)
(cd agents/supervisor && go test -count=1 ./...)
(cd devagent/filesnapshot && go test -count=1 ./...)
(cd devagent/gitdiff && go test -count=1 ./...)
(cd devagent/selfbootstrap && go test -count=1 ./...)
(cd devagent/workspace && go test -count=1 ./...)
(cd models/agnes && go test -count=1 ./...)
(cd models/ark && go test -count=1 ./...)
(cd models/openai && go test -count=1 ./...)
(cd tests/agents && go test -count=1 ./...)
