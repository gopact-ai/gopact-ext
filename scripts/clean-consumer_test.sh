#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

cat > "${tmp}/valid.txt" <<'EOF'
github.com/gopact-ai/gopact v0.1.0-rc.1 github.com/gopact-ai/gopact
github.com/gopact-ai/gopact-ext/models/openai v0.6.0-rc.1 github.com/gopact-ai/gopact-ext/models/openai/codex
github.com/gopact-ai/gopact-ext v0.6.0-rc.1 github.com/gopact-ai/gopact-ext/models/fake
github.com/gopact-ai/gopact-ext/stores v0.1.0-rc.1 github.com/gopact-ai/gopact-ext/stores/sqlite
github.com/gopact-ai/gopact-examples v0.1.0-rc.1 github.com/gopact-ai/gopact-examples/quickstart/model-basic
EOF

for count in 1 2 3 4 5; do
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

sed 's/v0.1.0-rc.1/v0.0.0/' "${tmp}/valid.txt" > "${tmp}/zero.txt"
expect_rejection "${tmp}/zero.txt"

for pseudo in \
	v2.0.0-20260714120000-0123456789ab \
	v1.2.4-0.20260714120000-0123456789ab \
	v1.2.4-rc.1.0.20260714120000-0123456789ab; do
	sed "1s/v0.1.0-rc.1/${pseudo}/" "${tmp}/valid.txt" > "${tmp}/pseudo.txt"
	expect_rejection "${tmp}/pseudo.txt"
done

sed '1s/v0.1.0-rc.1/v0.1.0-release.20260714120000-0123456789ab/' \
	"${tmp}/valid.txt" > "${tmp}/valid-timestamped-semver.txt"
"${script_dir}/clean-consumer.sh" --validate-only "${tmp}/valid-timestamped-semver.txt" >/dev/null

expect_rejection --prefix-count 0 "${tmp}/valid.txt"
expect_rejection --prefix-count 6 "${tmp}/valid.txt"

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
cat > "${tmp}/fake-bin/go" <<'EOF'
#!/usr/bin/env bash
if [[ "$1 $2" == "mod init" ]]; then
	printf 'module clean-consumer.example\n' > go.mod
	exit 0
fi
if [[ "$1 $2" == "mod download" ]]; then
	printf '{\n  "GoMod": "%s",\n  "Version": "v0.1.0-rc.1"\n}\n' "${TAGGED_GOMOD}"
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

echo "clean-consumer validation tests passed"
