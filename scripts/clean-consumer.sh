#!/usr/bin/env bash
set -euo pipefail

validate_only=false
if [[ "${1:-}" == "--validate-only" ]]; then
	validate_only=true
	shift
fi

manifest="${1:-}"
if [[ -z "${manifest}" || "${2:-}" != "" ]]; then
	echo "usage: $0 [--validate-only] <release-versions.txt>" >&2
	exit 2
fi
if [[ ! -f "${manifest}" ]]; then
	echo "manifest not found: ${manifest}" >&2
	exit 2
fi

expected=(
	github.com/gopact-ai/gopact
	github.com/gopact-ai/gopact-ext
	github.com/gopact-ai/gopact-ext/stores
	github.com/gopact-ai/gopact-examples
)
modules=()
versions=()

while read -r module version extra; do
	[[ -z "${module}" || "${module:0:1}" == "#" ]] && continue
	if [[ -z "${version:-}" || -n "${extra:-}" ]]; then
		echo "invalid manifest entry: ${module} ${version:-} ${extra:-}" >&2
		exit 1
	fi
	if [[ ! "${version}" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$ ]]; then
		echo "version must be an exact semantic version tag: ${module} ${version}" >&2
		exit 1
	fi
	if [[ "${version}" == "v0.0.0" || "${version}" =~ -[0-9.]*[0-9]{14}-[0-9a-f]{12}$ ]]; then
		echo "placeholder and pseudo-versions are forbidden: ${module} ${version}" >&2
		exit 1
	fi
	modules+=("${module}")
	versions+=("${version}")
done < "${manifest}"

if [[ "${#modules[@]}" -ne "${#expected[@]}" ]]; then
	echo "manifest must contain exactly ${#expected[@]} release modules" >&2
	exit 1
fi
for index in "${!expected[@]}"; do
	if [[ "${modules[${index}]}" != "${expected[${index}]}" ]]; then
		echo "manifest entry $((index + 1)) is ${modules[${index}]}; want ${expected[${index}]}" >&2
		exit 1
	fi
done

if [[ "${validate_only}" == true ]]; then
	echo "manifest is structurally valid"
	exit 0
fi

consumer="$(mktemp -d)"
trap 'rm -rf "${consumer}"' EXIT
cd "${consumer}"
go mod init clean-consumer.example

for index in "${!expected[@]}"; do
	module="${expected[${index}]}"
	version="${versions[${index}]}"
	case "${module}" in
		github.com/gopact-ai/gopact) package="${module}" ;;
		github.com/gopact-ai/gopact-ext) package="${module}/models/fake" ;;
		github.com/gopact-ai/gopact-ext/stores) package="${module}/sqlite" ;;
		github.com/gopact-ai/gopact-examples) package="${module}/quickstart/model-basic" ;;
	esac
	GOWORK=off go get "${package}@${version}"
done

cat > consumer_test.go <<'EOF'
package cleanconsumer

import (
	_ "github.com/gopact-ai/gopact"
	_ "github.com/gopact-ai/gopact-ext/models/fake"
	_ "github.com/gopact-ai/gopact-ext/stores/sqlite"
)
EOF

if grep -Eq '^[[:space:]]*replace([[:space:](]|$)' go.mod; then
	echo "clean consumer unexpectedly contains a replace directive" >&2
	exit 1
fi

for index in "${!expected[@]}"; do
	module="${expected[${index}]}"
	version="${versions[${index}]}"
	selected="$(GOWORK=off go list -m -f '{{.Version}}{{if .Replace}} replace{{end}}' "${module}")"
	if [[ "${selected}" != "${version}" ]]; then
		echo "selected ${module} ${selected}; want ${version}" >&2
		exit 1
	fi
done

GOWORK=off go mod verify
GOWORK=off go test ./...
