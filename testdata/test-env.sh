# Shared configuration for the test suites.
#
# Sourced by scripts, read by e2e/conftest.py, and mirrored in
# integration/setup_test.go via testconfig.go. Keeping the values here means the
# Go and Python suites cannot drift apart on the secret or the protected-path
# convention — a mismatch there produces confusing auth failures in one suite
# only.
#
# These are test values. They are deliberately committed and must never be used
# for a real deployment.

# Must be at least 32 characters (config.Finalize enforces this).
export YAAR_TEST_JWT_SECRET="testsecret123456789ö123456789123456789"

# A path the UI suite creates and expects to be write-protected.
export YAAR_TEST_PROTECTED_PATHS="/protected-dir"

# Port for the browser-driven suite's real server instance.
export YAAR_TEST_UI_PORT="8081"
