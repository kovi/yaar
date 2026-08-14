#!/bin/bash
set -e

# Change directory to the script's directory
cd "$(dirname "$0")"

echo "Running Biome Linter..."
./web/tools/biome check web/static/js/
echo "Linting passed successfully!"
