#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "$0")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT
cd "${repo_root}"

find . -name go.mod -not -path './.git/*' -print |
	while read -r modfile; do
		(cd "$(dirname "${modfile}")" && pwd -P)
	done |
	sort > "${tmp}/modules.txt"

go work edit -json |
	sed -n 's/.*"DiskPath": "\(.*\)".*/\1/p' |
	while read -r module_dir; do
		(cd "${module_dir}" && pwd -P)
	done |
	sort > "${tmp}/workspace.txt"

if ! diff -u "${tmp}/modules.txt" "${tmp}/workspace.txt"; then
	echo "go.work must include every module exactly once" >&2
	exit 1
fi

if [[ "$(grep -c '=>' go.work)" -ne 3 ]]; then
	echo "go.work must replace exactly the three unpublished dependency anchors" >&2
	exit 1
fi
for replacement in \
	"github.com/gopact-ai/gopact-ext v0.7.0 => ." \
	"github.com/gopact-ai/gopact-ext/agents/agenttool v0.2.0 => ./agents/agenttool" \
	"github.com/gopact-ai/gopact-ext/agents/internal v0.1.0 => ./agents/internal"; do
	if ! grep -Fq "${replacement}" go.work; then
		echo "go.work: missing replacement ${replacement}" >&2
		exit 1
	fi
done

while read -r module_dir; do
	modfile="${module_dir}/go.mod"
	relative="${module_dir#"${repo_root}"}"
	expected="github.com/gopact-ai/gopact-ext${relative}"
	actual="$(sed -n 's/^module //p' "${modfile}")"
	if [[ "${actual}" != "${expected}" ]]; then
		echo "${modfile}: module ${actual}; want ${expected}" >&2
		exit 1
	fi
	if ! grep -qx 'go 1.27' "${modfile}"; then
		echo "${modfile}: go version must be 1.27" >&2
		exit 1
	fi
	if grep -Eq '^[[:space:]]*replace([[:space:](]|$)' "${modfile}"; then
		echo "${modfile}: replace directives are forbidden" >&2
		exit 1
	fi
	while read -r dependency version; do
		[[ -n "${dependency}" ]] || continue
		if [[ ! "${version}" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ||
			"${version}" == "v0.0.0" ]]; then
			echo "${modfile}: unstable first-party dependency ${dependency} ${version}" >&2
			exit 1
		fi
	done < <(
		awk '$1 ~ /^github.com\/gopact-ai\/(gopact|gopact-ext)/ { print $1, $2 }' "${modfile}"
	)
done < "${tmp}/modules.txt"

require_exact() {
	local module_dir="$1"
	local dependency="$2"
	local version="$3"
	if ! awk -v dependency="${dependency}" -v version="${version}" \
		'$1 == dependency && $2 == version { found = 1 } END { exit !found }' \
		"${module_dir}/go.mod"; then
		echo "${module_dir}/go.mod: require ${dependency} ${version}" >&2
		exit 1
	fi
}

if grep -Eq '^[[:space:]]*require([[:space:](]|$)' go.mod; then
	echo "root go.mod must not require extension dependencies" >&2
	exit 1
fi

require_exact agents/internal github.com/gopact-ai/gopact v0.2.1
require_exact agents/internal github.com/gopact-ai/gopact-ext v0.7.0

for module_dir in \
	agents/agenttool \
	agents/loop \
	agents/parallel \
	agents/planexec \
	agents/react \
	agents/router \
	agents/sequential \
	agents/supervisor; do
	require_exact "${module_dir}" github.com/gopact-ai/gopact v0.2.1
	require_exact "${module_dir}" github.com/gopact-ai/gopact-ext/agents/internal v0.1.0
done
require_exact agents/react github.com/gopact-ai/gopact-ext/agents/agenttool v0.2.0

for module_dir in models/agnes models/fake models/glm; do
	require_exact "${module_dir}" github.com/gopact-ai/gopact v0.2.1
	require_exact "${module_dir}" github.com/gopact-ai/gopact-ext v0.7.0
done
require_exact models/agnes github.com/gopact-ai/gopact-ext/models/openai v0.6.0
require_exact models/glm github.com/gopact-ai/gopact-ext/models/openai v0.6.0

root_packages="$(GOWORK=off go list ./...)"
if [[ "${root_packages}" != "github.com/gopact-ai/gopact-ext/internal/carveout" ]]; then
	echo "root module packages = ${root_packages}; want internal/carveout only" >&2
	exit 1
fi

"${script_dir}/clean-consumer.sh" --validate-only "${script_dir}/release-versions.txt" >/dev/null
echo "module contract validation passed"
