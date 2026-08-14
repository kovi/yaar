#!/bin/bash
#
# Runs every test suite in this repository.
#
# The suites used to be invoked separately (`go test ./...` and the browser
# runner), which made it easy to ship a change that passed one and broke the
# other. Run this before pushing.
#
# Usage:
#   ./test.sh              # everything
#   ./test.sh unit         # Go unit + wiring tests only (fast)
#   ./test.sh integration  # Go integration suite only
#   ./test.sh e2e          # browser-driven suite only (slow, needs Playwright)
#   ./test.sh lint         # formatting and linters only
#
set -euo pipefail

cd "$(dirname "$0")"

# Shared settings for both suites (JWT secret, protected paths, UI port).
# shellcheck source=testdata/test-env.sh
source testdata/test-env.sh

TARGET="${1:-all}"

# Colours only when attached to a terminal, so CI logs stay clean.
if [ -t 1 ]; then
    BOLD=$'\033[1m'; GREEN=$'\033[32m'; RED=$'\033[31m'; RESET=$'\033[0m'
else
    BOLD=""; GREEN=""; RED=""; RESET=""
fi

step() { echo; echo "${BOLD}==> $*${RESET}"; }
ok()   { echo "${GREEN}✓ $*${RESET}"; }
fail() { echo "${RED}✗ $*${RESET}" >&2; }

FAILED=()

run_step() {
    local name="$1"; shift
    step "$name"
    if "$@"; then
        ok "$name passed"
    else
        fail "$name FAILED"
        FAILED+=("$name")
    fi
}

run_lint() {
    step "Checking Go formatting"
    local unformatted
    unformatted=$(gofmt -l . 2>/dev/null | grep -v '^e2e/venv/' || true)
    if [ -n "$unformatted" ]; then
        fail "these files need gofmt:"
        echo "$unformatted"
        FAILED+=("gofmt")
    else
        ok "gofmt clean"
    fi

    run_step "go vet" go vet ./...
    run_step "Biome (web assets)" ./lint.sh
}

run_unit() {
    # Everything except the integration suite: fast, no browser required.
    # This includes the main-package wiring and graceful-shutdown tests.
    run_step "Go unit + wiring tests" \
        go test $(go list ./... | grep -v '/integration$')
}

run_integration() {
    run_step "Go integration suite" go test ./integration/
}

run_e2e() {
    step "Browser end-to-end suite"
    if ./e2e/run.sh; then
        ok "Browser end-to-end suite passed"
    else
        fail "Browser end-to-end suite FAILED"
        FAILED+=("e2e")
    fi
}

case "$TARGET" in
    all)         run_lint; run_unit; run_integration; run_e2e ;;
    lint)        run_lint ;;
    unit)        run_unit ;;
    integration) run_integration ;;
    e2e)         run_e2e ;;
    *)
        echo "Unknown target: $TARGET" >&2
        echo "Usage: $0 [all|lint|unit|integration|e2e]" >&2
        exit 2
        ;;
esac

echo
if [ ${#FAILED[@]} -eq 0 ]; then
    echo "${GREEN}${BOLD}All checks passed.${RESET}"
    exit 0
fi

echo "${RED}${BOLD}Failed: ${FAILED[*]}${RESET}"
exit 1
