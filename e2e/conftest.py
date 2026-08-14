import pytest
import subprocess
import time
import os
import re
import shutil
import socket

def is_port_open(port):
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        return s.connect_ex(('localhost', port)) == 0


def load_shared_test_env():
    """Read testdata/test-env.sh, the settings shared with the Go suites.

    The JWT secret and protected-path convention used to be hardcoded here
    *and* in the Go setup, so changing one produced auth failures only in the
    other suite. Both now read this file.
    """
    root = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
    path = os.path.join(root, "testdata", "test-env.sh")

    values = {}
    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            line = line.removeprefix("export ")
            if "=" not in line:
                continue
            key, _, value = line.partition("=")
            values[key.strip()] = value.strip().strip("\"'")
    return values


SHARED_ENV = load_shared_test_env()


def read_admin_password(log_path, timeout=10):
    """Extract the bootstrap admin password from the server's startup output.

    A fresh instance generates a random admin password and prints it once, so
    there is no fixed credential for the UI tests to hardcode. The banner looks
    like:

        password: k3Jq8vN2xPbR7mZtLdW4yF1s

    The log is polled because the banner is written during startup, which may
    not have flushed by the time the port opens.
    """
    pattern = re.compile(r"^\s*password:\s*(\S+)\s*$", re.MULTILINE)

    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with open(log_path, "r") as f:
                match = pattern.search(f.read())
                if match:
                    return match.group(1)
        except FileNotFoundError:
            pass
        time.sleep(0.2)

    raise RuntimeError(
        f"Could not find the generated admin password in {log_path}. "
        "The bootstrap banner is printed only when the user table is empty — "
        "check that the test workdir was cleaned."
    )

@pytest.fixture(scope="session", autouse=True)
def go_server():
    # Define sandboxed resources inside the e2e directory
    workdir = "test-workdir"
    db_path = "test_artifactory.db"
    storage_dir = "test_storage"
    port = int(SHARED_ENV["YAAR_TEST_UI_PORT"])

    # Cleanup any leftovers
    if os.path.exists(workdir):
        shutil.rmtree(workdir)

    os.makedirs(os.path.join(workdir, storage_dir, "protected-dir"), exist_ok=True)

    # Build the Go binary directly so we can run the actual executable (not the 'go run' wrapper).
    # Build the whole package ("."), not main.go alone: the entrypoint is split
    # across main.go and app.go, and naming a single file silently drops the rest.
    root_dir = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
    binary_name = "yaar-test-bin"
    binary_path = os.path.abspath(os.path.join(workdir, binary_name))
    build_cmd = ["go", "build", "-o", binary_path, "."]
    print(f"\nBuilding Go backend binary: {' '.join(build_cmd)}")
    subprocess.run(build_cmd, cwd=root_dir, check=True)

    # Start the Go server pointing to the compiled binary
    cmd = [
        binary_path,
        "-port", str(port),
        "-db", db_path,
        "-data-dir", storage_dir,
        "-web-dir", os.path.join(root_dir, "web")
    ]
    
    env = os.environ.copy()
    env["JWT_SECRET"] = SHARED_ENV["YAAR_TEST_JWT_SECRET"]
    env["AF_PROTECTED_PATHS"] = SHARED_ENV["YAAR_TEST_PROTECTED_PATHS"]
    
    print(f"Starting Go backend: {' '.join(cmd)}")
    log_file = open(os.path.join(workdir, "yaar.log"), "w")
    server_process = subprocess.Popen(
        cmd,
        cwd=workdir,
        env=env,
        stdout=log_file,
        stderr=subprocess.STDOUT,
        text=True
    )

    # Wait for the port to become active (up to 10 seconds)
    started = False
    for _ in range(20):
        # If the process has already exited, it failed to start
        ret = server_process.poll()
        if ret is not None:
            break

        if is_port_open(port):
            # Short sleep to ensure the process didn't bind but crash immediately after
            time.sleep(0.2)
            if server_process.poll() is None:
                started = True
                break
        time.sleep(0.5)

    if not started:
        # Check if the process exited early and print error
        ret = server_process.poll()
        stdout = ""
        try:
            with open(os.path.join(workdir, "yaar.log"), "r") as f:
                stdout = f.read()
        except:
            pass
        server_process.kill()
        raise RuntimeError(
            f"Go server failed to start on port {port}. Exit code: {ret}\nLOG:\n{stdout}"
        )

    # Stash the generated admin password for the admin_password fixture.
    global _admin_password
    _admin_password = read_admin_password(os.path.join(workdir, "yaar.log"))

    yield f"http://localhost:{port}"

    # Tear down
    server_process.kill()
    server_process.wait()


# Set by the go_server fixture once the server has bootstrapped.
_admin_password = None


@pytest.fixture(scope="session")
def admin_password(go_server):
    """The randomly generated password for the bootstrap admin user.

    Fresh instances no longer ship a fixed default, so tests must read the
    password the server actually generated instead of hardcoding one.
    """
    assert _admin_password, "the admin password was not captured at startup"
    return _admin_password
