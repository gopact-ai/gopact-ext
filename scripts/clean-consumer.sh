#!/usr/bin/env bash
set -euo pipefail

validate_only=false
prefix_count=""
manifest=""
while [[ "$#" -gt 0 ]]; do
	case "$1" in
		--validate-only) validate_only=true ;;
		--prefix-count)
			shift
			prefix_count="${1:-}"
			;;
		-*) echo "unknown option: $1" >&2; exit 2 ;;
		*)
			[[ -z "${manifest}" ]] || { echo "only one manifest may be supplied" >&2; exit 2; }
			manifest="$1"
			;;
	esac
	shift
done

if [[ -z "${manifest}" ]]; then
	echo "usage: $0 [--validate-only] [--prefix-count N] <release-versions.txt>" >&2
	exit 2
fi
if [[ ! -f "${manifest}" ]]; then
	echo "manifest not found: ${manifest}" >&2
	exit 2
fi

modules=()
versions=()
packages=()

while read -r module version package extra; do
	[[ -z "${module}" || "${module:0:1}" == "#" ]] && continue
	if [[ -z "${version:-}" || -z "${package:-}" || -n "${extra:-}" ]]; then
		echo "invalid manifest entry: ${module} ${version:-} ${package:-} ${extra:-}" >&2
		exit 1
	fi
	if [[ "${package}" != "-" && "${package}" != "${module}" && "${package}" != "${module}/"* ]]; then
		echo "check package ${package} is outside module ${module}" >&2
		exit 1
	fi
	if [[ ! "${version}" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
		echo "version must be an exact stable semantic version tag: ${module} ${version}" >&2
		exit 1
	fi
	if [[ "${version}" == "v0.0.0" ]]; then
		echo "placeholder versions are forbidden: ${module} ${version}" >&2
		exit 1
	fi
	if [[ -n "${modules[*]-}" ]]; then
		for existing in "${modules[@]}"; do
			if [[ "${existing}" == "${module}" ]]; then
				echo "duplicate manifest module: ${module}" >&2
				exit 1
			fi
		done
	fi
	modules+=("${module}")
	versions+=("${version}")
	packages+=("${package}")
done < "${manifest}"

if [[ "${#modules[@]}" -eq 0 ]]; then
	echo "manifest must contain at least one release module" >&2
	exit 1
fi
[[ -n "${prefix_count}" ]] || prefix_count="${#modules[@]}"
if [[ ! "${prefix_count}" =~ ^[1-9][0-9]*$ || "${prefix_count}" -gt "${#modules[@]}" ]]; then
	echo "--prefix-count must be between 1 and ${#modules[@]}" >&2
	exit 2
fi

if [[ "${validate_only}" == true ]]; then
	echo "manifest is structurally valid for release prefix ${prefix_count}"
	exit 0
fi

consumer="$(mktemp -d)"
trap 'rm -rf "${consumer}"' EXIT
cd "${consumer}"
go mod init clean-consumer.example

imports=()
for ((index = 0; index < prefix_count; index++)); do
	module="${modules[${index}]}"
	version="${versions[${index}]}"
	package="${packages[${index}]}"
	download="$(GOWORK=off go mod download -json "${module}@${version}")"
	module_gomod="$(printf '%s\n' "${download}" | sed -n 's/^[[:space:]]*"GoMod": "\(.*\)",$/\1/p')"
	if [[ -z "${module_gomod}" || ! -f "${module_gomod}" ]]; then
		echo "downloaded module has no readable go.mod: ${module}@${version}" >&2
		exit 1
	fi
	if grep -Eq '^[[:space:]]*replace([[:space:](]|$)' "${module_gomod}"; then
		echo "tagged module contains a replace directive: ${module}@${version}" >&2
		exit 1
	fi
	if [[ "${package}" == "-" ]]; then
		GOWORK=off go mod edit -require="${module}@${version}"
		continue
	fi
	GOWORK=off go get "${package}@${version}"
	if [[ "$(GOWORK=off go list -f '{{.Name}}' "${package}")" == "main" ]]; then
		GOWORK=off go test -run '^$' "${package}"
	else
		imports+=("${package}")
	fi
done

{
	echo 'package cleanconsumer'
	if [[ -n "${imports[*]-}" ]]; then
		echo 'import ('
		for package in "${imports[@]}"; do
			printf '\t_ "%s"\n' "${package}"
		done
		echo ')'
	fi
} > consumer_test.go

if grep -Eq '^[[:space:]]*replace([[:space:](]|$)' go.mod; then
	echo "clean consumer unexpectedly contains a replace directive" >&2
	exit 1
fi

for ((index = 0; index < prefix_count; index++)); do
	module="${modules[${index}]}"
	version="${versions[${index}]}"
	selected="$(GOWORK=off go list -m -f '{{.Version}}{{if .Replace}} replace{{end}}' "${module}")"
	if [[ "${selected}" != "${version}" ]]; then
		echo "selected ${module} ${selected}; want ${version}" >&2
		exit 1
	fi
done

GOWORK=off go mod verify
GOWORK=off go test ./...
