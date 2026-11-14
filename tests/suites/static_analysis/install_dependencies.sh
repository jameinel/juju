run_install_dependencies_compatibility() {
	local fixture="${TEST_DIR}/install-dependencies"
	local real_yq
	real_yq=$(command -v yq)
	mkdir -p "${fixture}/bin" "${fixture}/gobin"

	cat >"${fixture}/bin/yq" <<'EOF'
#!/bin/sh
case "${YQ_TEST_MODE:-}" in
	bad-input) [ "$1" != "-p=json" ] || exit 1 ;;
	bad-output) [ "$1" != "-o=json" ] || exit 1 ;;
	failing-output) printf 'ok\n'; exit 1 ;;
	failing-json-output) if [ "$1" = "-o=json" ]; then printf '"ok"\n'; exit 1; fi ;;
esac
exec "$REAL_YQ" "$@"
EOF
	cat >"${fixture}/bin/golangci-lint" <<'EOF'
#!/bin/sh
if [ "${LINT_TEST_VERSION:-}" = fail ]; then
	exit 1
fi
printf 'golangci-lint has version %s\n' "${LINT_TEST_VERSION:-2.14.0}"
EOF
	cat >"${fixture}/gobin/golangci-lint" <<'EOF'
#!/bin/sh
printf 'golangci-lint has version 2.14.0\n'
EOF
	chmod +x "${fixture}/bin/yq" "${fixture}/bin/golangci-lint"
	chmod +x "${fixture}/gobin/golangci-lint"
	export REAL_YQ="${real_yq}" GOBIN="${fixture}/gobin"
	export PATH="${fixture}/bin:${PATH}"

	make install-snap-dependencies GO_INSTALLED_VERSION=999
	local mode output
	for mode in bad-input bad-output failing-output failing-json-output; do
		if output=$(YQ_TEST_MODE="${mode}" make install-snap-dependencies GO_INSTALLED_VERSION=999 2>&1); then
			echo "Expected yq ${mode} to be rejected" >&2
			return 1
		fi
		echo "${output}" | grep -q 'Incompatible yq on PATH'
	done

	local tools='github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0'
	make install-development-go-tools GO_DEVELOPMENT_TOOLS="${tools}"
	LINT_TEST_VERSION=2.14.9 make install-development-go-tools GO_DEVELOPMENT_TOOLS="${tools}"
	for mode in 2.13.0 2.15.0 malformed fail; do
		if output=$(LINT_TEST_VERSION="${mode}" make install-development-go-tools GO_DEVELOPMENT_TOOLS="${tools}" 2>&1); then
			echo "Expected golangci-lint ${mode} to be rejected" >&2
			return 1
		fi
		echo "${output}" | grep -q 'golangci-lint.*PATH'
	done
}

test_install_dependencies_compatibility() {
	if [ "$(skip 'test_install_dependencies_compatibility')" ]; then
		echo "==> TEST SKIPPED: install dependencies compatibility"
		return
	fi

	(
		cd .. || exit
		run "run_install_dependencies_compatibility"
	)
}
