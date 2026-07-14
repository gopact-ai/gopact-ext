#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

cat > "${tmp}/valid.txt" <<'EOF'
github.com/gopact-ai/gopact v0.1.0-rc.1
github.com/gopact-ai/gopact-ext v0.6.0-rc.1
github.com/gopact-ai/gopact-ext/stores v0.1.0-rc.1
github.com/gopact-ai/gopact-examples v0.1.0-rc.1
EOF

for count in 1 2 3 4; do
	"${script_dir}/clean-consumer.sh" --validate-only --prefix-count "${count}" "${tmp}/valid.txt" >/dev/null
done

expect_rejection() {
	if "${script_dir}/clean-consumer.sh" --validate-only "$@" >/dev/null 2>&1; then
		echo "expected rejection: $*" >&2
		exit 1
	fi
}

sed 's/v0.1.0-rc.1/v0.0.0/' "${tmp}/valid.txt" > "${tmp}/zero.txt"
expect_rejection "${tmp}/zero.txt"

sed 's/v0.1.0-rc.1/v0.1.1-0.20260714120000-0123456789ab/' "${tmp}/valid.txt" > "${tmp}/pseudo.txt"
expect_rejection "${tmp}/pseudo.txt"

expect_rejection --prefix-count 0 "${tmp}/valid.txt"

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
