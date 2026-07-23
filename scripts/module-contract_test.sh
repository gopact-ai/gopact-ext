#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "$0")" && pwd -P)"
repo_root="$(cd "${script_dir}/.." && pwd -P)"
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

{
	printf '%s\n' "${repo_root}"
	make -s --no-print-directory print-workspace-modules |
		while read -r module_dir; do
			(cd "${module_dir}" && pwd -P)
		done
} | sort > "${tmp}/makefile-modules.txt"

if ! diff -u "${tmp}/modules.txt" "${tmp}/makefile-modules.txt"; then
	echo "WORKSPACE_MODULES must include every nested module exactly once" >&2
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

: > "${tmp}/publishable-modules.txt"
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
		awk '
			$1 == "require" &&
				$2 ~ /^github.com\/gopact-ai\/(gopact|gopact-ext)/ {
				print $2, $3
				next
			}
			$1 ~ /^github.com\/gopact-ai\/(gopact|gopact-ext)/ {
				print $1, $2
			}
		' "${modfile}"
	)
	if [[ "${relative}" != "/tests/workflow" ]]; then
		printf '%s\n' "${actual}" >> "${tmp}/publishable-modules.txt"
	fi
done < "${tmp}/modules.txt"
sort -o "${tmp}/publishable-modules.txt" "${tmp}/publishable-modules.txt"

awk '
	$1 ~ /^github.com\/gopact-ai\/gopact-ext($|\/)/ { print $1 }
' "${script_dir}/release-versions.txt" |
	sort > "${tmp}/manifest-modules.txt"

if ! diff -u "${tmp}/publishable-modules.txt" "${tmp}/manifest-modules.txt"; then
	echo "release manifest must include every publishable extension module exactly once" >&2
	exit 1
fi

require_exact() {
	local module_dir="$1"
	local dependency="$2"
	local version="$3"
	if ! awk -v dependency="${dependency}" -v version="${version}" \
		'($1 == dependency && $2 == version) ||
			($1 == "require" && $2 == dependency && $3 == version) {
				found = 1
			}
			END { exit !found }' \
		"${module_dir}/go.mod"; then
		echo "${module_dir}/go.mod: require ${dependency} ${version}" >&2
		exit 1
	fi
}

require_carveout_import() {
	local file="$1"
	if ! grep -Fqx 'import _ "github.com/gopact-ai/gopact-ext/internal/carveout"' "${file}"; then
		echo "${file}: missing root carve-out anchor" >&2
		exit 1
	fi
}

manifest_position() {
	local module="$1"
	awk -v module="${module}" \
		'$1 == module { print NR; found = 1 } END { exit !found }' \
		"${script_dir}/release-versions.txt"
}

require_manifest_before() {
	local prerequisite="$1"
	local dependent="$2"
	local prerequisite_line
	local dependent_line
	prerequisite_line="$(manifest_position "${prerequisite}")"
	dependent_line="$(manifest_position "${dependent}")"
	if ((prerequisite_line >= dependent_line)); then
		echo "release manifest: ${prerequisite} must precede ${dependent}" >&2
		exit 1
	fi
}

if grep -Eq '^[[:space:]]*require([[:space:](]|$)' go.mod; then
	echo "root go.mod must not require extension dependencies" >&2
	exit 1
fi

require_exact agents/internal github.com/gopact-ai/gopact v0.2.1
require_exact agents/internal github.com/gopact-ai/gopact-ext v0.7.0
require_carveout_import agents/internal/contract/carveout_test.go

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
	require_exact "${module_dir}" github.com/gopact-ai/gopact-ext v0.7.0
	require_exact "${module_dir}" github.com/gopact-ai/gopact-ext/agents/internal v0.1.0
	require_carveout_import "${module_dir}/carveout_test.go"
done
require_exact agents/react github.com/gopact-ai/gopact-ext/agents/agenttool v0.2.0

for module_dir in models/agnes models/fake models/glm; do
	require_exact "${module_dir}" github.com/gopact-ai/gopact v0.2.1
	require_exact "${module_dir}" github.com/gopact-ai/gopact-ext v0.7.0
	require_carveout_import "${module_dir}/carveout_test.go"
done
require_exact models/agnes github.com/gopact-ai/gopact-ext/models/openai v0.6.0
require_exact models/glm github.com/gopact-ai/gopact-ext/models/openai v0.6.0

core_module="github.com/gopact-ai/gopact"
root_module="github.com/gopact-ai/gopact-ext"
internal_module="${root_module}/agents/internal"
agenttool_module="${root_module}/agents/agenttool"
openai_module="${root_module}/models/openai"
examples_module="github.com/gopact-ai/gopact-examples"

while read -r extension_module; do
	require_manifest_before "${core_module}" "${extension_module}"
	require_manifest_before "${extension_module}" "${examples_module}"
done < "${tmp}/manifest-modules.txt"

for module_dir in \
	agents/internal \
	agents/agenttool \
	agents/loop \
	agents/parallel \
	agents/planexec \
	agents/react \
	agents/router \
	agents/sequential \
	agents/supervisor \
	models/agnes \
	models/fake \
	models/glm; do
	require_manifest_before "${root_module}" \
		"github.com/gopact-ai/gopact-ext/${module_dir}"
done

for module_dir in \
	agents/agenttool \
	agents/loop \
	agents/parallel \
	agents/planexec \
	agents/react \
	agents/router \
	agents/sequential \
	agents/supervisor; do
	require_manifest_before "${internal_module}" \
		"github.com/gopact-ai/gopact-ext/${module_dir}"
done
require_manifest_before "${agenttool_module}" \
	"github.com/gopact-ai/gopact-ext/agents/react"
require_manifest_before "${openai_module}" \
	"github.com/gopact-ai/gopact-ext/models/agnes"
require_manifest_before "${openai_module}" \
	"github.com/gopact-ai/gopact-ext/models/glm"

root_packages="$(GOWORK=off go list ./...)"
if [[ "${root_packages}" != "github.com/gopact-ai/gopact-ext/internal/carveout" ]]; then
	echo "root module packages = ${root_packages}; want internal/carveout only" >&2
	exit 1
fi

"${script_dir}/clean-consumer.sh" --validate-only "${script_dir}/release-versions.txt" >/dev/null
echo "module contract validation passed"
