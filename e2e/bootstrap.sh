#!/bin/bash
set -e

# Change directory to the script's directory
cd "$(dirname "$0")"

# A venv is only usable if its interpreter can still import what pip installed
# *and* its console scripts still point at the right interpreter. Checking for
# venv/bin/pip alone is not enough, for two reasons seen in practice:
#
#   1. After a system Python upgrade (3.13 -> 3.14) the symlinked interpreter
#      looks in lib/python3.14/site-packages while every package still sits in
#      lib/python3.13/, so the venv appears intact but nothing imports.
#   2. A venv is not relocatable: its scripts hardcode an absolute shebang, so
#      renaming the containing directory leaves `./venv/bin/pip` failing with
#      "bad interpreter".
#
# Probe for both and rebuild from scratch when stale.
venv_is_usable() {
    [ -x "venv/bin/python3" ] || return 1
    ./venv/bin/python3 -c "import pip" >/dev/null 2>&1 || return 1
    # Catches the relocation case: the script runs only if its shebang resolves.
    ./venv/bin/pip --version >/dev/null 2>&1 || return 1
}

if ! venv_is_usable; then
    if [ -d "venv" ]; then
        echo "Existing venv is unusable (Python upgrade or the directory was renamed) - rebuilding..."
        rm -rf venv
    fi

    echo "Creating virtual environment..."
    python3 -m venv --without-pip venv

    echo "Bootstrapping pip..."
    python3 -c "import urllib.request; urllib.request.urlretrieve('https://bootstrap.pypa.io/get-pip.py', 'get-pip.py')"
    ./venv/bin/python3 get-pip.py
    rm -f get-pip.py
fi

echo "Installing requirements..."
./venv/bin/pip install -r requirements.txt

echo "Installing browser..."
./venv/bin/playwright install firefox
echo "Bootstrap complete!"
