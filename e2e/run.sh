#!/bin/bash
set -e

# Change directory to the script's directory
cd "$(dirname "$0")"

# Remove test-workdir if it exists
rm -rf test-workdir

# Bootstrap
./bootstrap.sh

# Run pytest
# (linting is handled by ../test.sh, which runs it once for the whole repo)
echo "Running Playwright browser UI tests..."
# --headed --slowmo 500
./venv/bin/pytest -v --browser firefox "$@"
