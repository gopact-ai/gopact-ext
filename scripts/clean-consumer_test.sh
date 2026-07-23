#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

cat > "${tmp}/valid.txt" <<'EOF'
github.com/gopact-ai/gopact v0.2.1 github.com/gopact-ai/gopact
github.com/gopact-ai/gopact-ext/models/openai v0.6.0 github.com/gopact-ai/gopact-ext/models/openai/codex
github.com/gopact-ai/gopact-ext v0.7.0 -
github.com/gopact-ai/gopact-ext/agents/internal v0.1.0 -
github.com/gopact-ai/gopact-ext/stores v0.2.0 github.com/gopact-ai/gopact-ext/stores/sqlite
github.com/gopact-ai/gopact-examples v0.2.0 github.com/gopact-ai/gopact-examples/quickstart/model-basic
EOF

entry_count="$(grep -cvE '^[[:space:]]*(#|$)' "${tmp}/valid.txt")"
for ((count = 1; count <= entry_count; count++)); do
	"${script_dir}/clean-consumer.sh" --validate-only --prefix-count "${count}" "${tmp}/valid.txt" >/dev/null
done
head -n 2 "${tmp}/valid.txt" > "${tmp}/short.txt"
"${script_dir}/clean-consumer.sh" --validate-only "${tmp}/short.txt" >/dev/null

expect_rejection() {
	if "${script_dir}/clean-consumer.sh" --validate-only "$@" >/dev/null 2>&1; then
		echo "expected rejection: $*" >&2
		exit 1
	fi
}

sed '1s/v0.2.1/v0.0.0/' "${tmp}/valid.txt" > "${tmp}/zero.txt"
expect_rejection "${tmp}/zero.txt"

for unstable in \
	v0.2.1-rc.1 \
	v2.0.0-20260714120000-0123456789ab \
	v1.2.4-0.20260714120000-0123456789ab \
	v1.2.4-rc.1.0.20260714120000-0123456789ab \
	v0.1.0-release.20260714120000-0123456789ab; do
	sed "1s/v0.2.1/${unstable}/" "${tmp}/valid.txt" > "${tmp}/unstable.txt"
	expect_rejection "${tmp}/unstable.txt"
done

expect_rejection --prefix-count 0 "${tmp}/valid.txt"
expect_rejection --prefix-count "$((entry_count + 1))" "${tmp}/valid.txt"

sed '2s#github.com/gopact-ai/gopact-ext/models/openai/codex$#example.com/outside#' \
	"${tmp}/valid.txt" > "${tmp}/outside-package.txt"
expect_rejection "${tmp}/outside-package.txt"

sed -n '1p;1p' "${tmp}/valid.txt" > "${tmp}/duplicate.txt"
expect_rejection "${tmp}/duplicate.txt"

mkdir "${tmp}/fake-bin"
cat > "${tmp}/tagged.mod" <<'EOF'
module github.com/gopact-ai/gopact
replace example.invalid/dependency => ../local
EOF
cat > "${tmp}/clean.mod" <<'EOF'
module github.com/gopact-ai/gopact-ext
EOF
cat > "${tmp}/fake-bin/go" <<'EOF'
#!/usr/bin/env bash
if [[ "$1 $2" == "mod init" ]]; then
	if [[ -n "${REQUIRE_EXTERNAL_CACHE:-}" && "${GOMODCACHE}" == "${PWD}/"* ]]; then
		echo "clean consumer placed module cache inside the consumer module" >&2
		exit 92
	fi
	if [[ -n "${CALLER_GOMODCACHE:-}" && "${GOMODCACHE:-}" == "${CALLER_GOMODCACHE}" ]]; then
		echo "clean consumer reused caller module cache" >&2
		exit 98
	fi
	if [[ -n "${CALLER_GOWORK:-}" && "${GOWORK:-}" != "off" ]]; then
		echo "clean consumer reused caller workspace" >&2
		exit 97
	fi
	if [[ -n "${CALLER_GOFLAGS:-}" && -n "${GOFLAGS:-}" ]]; then
		echo "clean consumer reused caller Go flags" >&2
		exit 96
	fi
	if [[ -n "${CALLER_GO111MODULE:-}" && "${GO111MODULE:-}" != "on" ]]; then
		echo "clean consumer reused caller module mode" >&2
		exit 95
	fi
	if [[ -n "${CALLER_GOENV:-}" && "${GOENV:-}" != "off" ]]; then
		echo "clean consumer reused caller Go environment" >&2
		exit 94
	fi
	printf 'module clean-consumer.example\n' > go.mod
	exit 0
fi
if [[ "$1 $2" == "mod download" ]]; then
	if [[ "${3:-}" == "all" ]]; then
		if [[ -n "${EXPECTED_GRAPH_REQUIRE:-}" ]] &&
			! grep -Fqx "${EXPECTED_GRAPH_REQUIRE}" go.mod; then
			echo "clean consumer downloaded an incomplete module graph" >&2
			exit 91
		fi
		[[ -z "${GRAPH_DOWNLOAD_RECORD:-}" ]] || : > "${GRAPH_DOWNLOAD_RECORD}"
		exit 0
	fi
	if [[ -n "${DOWNLOAD_FAILURE:-}" ]]; then
		printf '{"Error":"proxy unavailable"}\n'
		exit 1
	fi
	printf '{\n  "GoMod": "%s",\n  "Version": "%s"\n}\n' \
		"${TAGGED_GOMOD}" "${DOWNLOAD_VERSION:-v0.2.1}"
	exit 0
fi
if [[ "$1 $2" == "mod edit" ]]; then
	requirement="${3#-require=}"
	printf 'require %s %s\n' "${requirement%@*}" "${requirement##*@}" >> go.mod
	exit 0
fi
if [[ "$1 $2" == "list -m" ]]; then
	if [[ -n "${REQUIRE_POST_TEST_VERSION_CHECK:-}" &&
		! -f "${MOD_UPDATE_RECORD}" ]]; then
		echo "clean consumer checked versions before module graph updates" >&2
		exit 90
	fi
	printf '%s\n' "${EXPECTED_VERSION}"
	exit 0
fi
if [[ "$1" == "test" && -n "${REQUIRE_MOD_UPDATE:-}" && "${2:-}" != "-mod=mod" ]]; then
	echo "clean consumer did not allow module graph updates" >&2
	exit 93
fi
if [[ "$1" == "test" && -n "${MOD_UPDATE_RECORD:-}" ]]; then
	: > "${MOD_UPDATE_RECORD}"
fi
if [[ "$1 $2" == "mod verify" || "$1" == "test" ]]; then
	exit 0
fi
exit 99
EOF
chmod +x "${tmp}/fake-bin/go"
if PATH="${tmp}/fake-bin:${PATH}" TAGGED_GOMOD="${tmp}/tagged.mod" \
	"${script_dir}/clean-consumer.sh" --prefix-count 1 "${tmp}/valid.txt" >/dev/null 2>&1; then
	echo "expected tagged-module replace rejection" >&2
	exit 1
fi
if download_error="$(
	PATH="${tmp}/fake-bin:${PATH}" \
		TAGGED_GOMOD="${tmp}/clean.mod" \
		DOWNLOAD_FAILURE=1 \
		"${script_dir}/clean-consumer.sh" --prefix-count 1 "${tmp}/valid.txt" 2>&1
)"; then
	echo "expected module download failure" >&2
	exit 1
fi
if [[ "${download_error}" != *"proxy unavailable"* ]]; then
	echo "module download failure omitted proxy diagnostics" >&2
	exit 1
fi

cat > "${tmp}/module-only.txt" <<'EOF'
github.com/gopact-ai/gopact-ext v0.7.0 -
EOF
graph_download_record="${tmp}/module-graph-downloaded"
mod_update_record="${tmp}/module-graph-updated"
PATH="${tmp}/fake-bin:${PATH}" \
	TAGGED_GOMOD="${tmp}/clean.mod" \
	DOWNLOAD_VERSION="v0.7.0" \
	EXPECTED_VERSION="v0.7.0" \
	GOMODCACHE="${tmp}/warm-module-cache" \
	CALLER_GOMODCACHE="${tmp}/warm-module-cache" \
	GOWORK="${tmp}/hostile.work" \
	CALLER_GOWORK="${tmp}/hostile.work" \
	GOFLAGS="-mod=vendor" \
	CALLER_GOFLAGS="-mod=vendor" \
	GO111MODULE=off \
	CALLER_GO111MODULE=off \
	GOENV="${tmp}/hostile-goenv" \
	CALLER_GOENV="${tmp}/hostile-goenv" \
	EXPECTED_GRAPH_REQUIRE="require github.com/gopact-ai/gopact-ext v0.7.0" \
	GRAPH_DOWNLOAD_RECORD="${graph_download_record}" \
	MOD_UPDATE_RECORD="${mod_update_record}" \
	REQUIRE_POST_TEST_VERSION_CHECK=1 \
	REQUIRE_MOD_UPDATE=1 \
	REQUIRE_EXTERNAL_CACHE=1 \
	"${script_dir}/clean-consumer.sh" "${tmp}/module-only.txt" >/dev/null
if [[ ! -f "${graph_download_record}" ]]; then
	echo "clean consumer did not download the completed module graph" >&2
	exit 1
fi

echo "clean-consumer validation tests passed"
