# Yet Another Artifactory

A Go-based artifact server that serves and manages files from a real filesystem directory.  
Built with a Go (Gin/GORM/SQLite) backend and a lightweight Vanilla JS SPA frontend.

## Key Features

- **Dual Authentication:** JWT-based login and hashed API Tokens.
- **Scoped Permissions:** API Tokens can be restricted to specific directory prefixes (e.g., `/builds/ci/*`).
- **Lifecycle Policies:**
  - **TTL:** Automatic file expiry via `X-Expires` (supports durations like `7d` or `24h`).
  - **KeepLatest:** Automatic rotation of stream groups; keeps only the most recent version.
  - **Protected Paths:** Append-only directories defined in system configuration.
- **Data Integrity:** Real-time calculation and verification of SHA256, SHA1, and MD5 checksums.
- **Auto Sync:** Background reconciler syncs manual filesystem changes back to the database.
- **Global Search:** Lookup by filename, path, tags, or stream identifiers.
- **Audit Logging:** actions are recorded in a dedicated JSON audit trail.

## API Endpoints

### 1. File Access

Standard HTTP verbs at the root level for high compatibility with tools like `curl`, `wget`, and `maven`.

| Method   | Endpoint | Description                                                    |
|:---------|:---------|:---------------------------------------------------------------|
| `GET`    | `/*path` | Returns **Raw File** Supports `Range`.                         |
| `HEAD`   | `/*path` | Returns metadata headers (Size, Checksums, Type).              |
| `PUT`    | `/*path` | **Raw Stream Upload**. Creates/Overwrites file at target path. |
| `DELETE` | `/*path` | **Physical Delete**. Removes file and associated DB metadata.  |

**Headers (GET/HEAD):**

- `X-Checksum-Sha256`, `X-Checksum-Sha1`, `X-Checksum-Md5`
- `ETag`: Contains the SHA256 hash.

### 2. Uploads & Automation

Supports both raw binary streams and standard multipart forms.

| Method | Endpoint             | Description                                                     |
|:-------|:---------------------|:----------------------------------------------------------------|
| `PUT`  | `/*path`             | Raw binary body upload to specific path.                        |
| `POST` | `/_/api/v1/fs/*path` | Multipart Form upload (`file` field). Directory taken from URL. |

**Policy Headers:**

- `X-API-Token`: Required for non-browser automation.
- `X-Stream`: Format `stream-name/group-id` (e.g. `frontend/v1.0.4`).
- `X-Expires`: Duration (e.g. `30d`, `12h`) or ISO8601 date.
- `X-KeepLatest`: `true` to mark previous groups in this stream as expired.
- `X-Tags`: Comma/Semicolon separated list (e.g. `env=prod, arch=x64`).
- `X-Checksum-Sha256`: Optional client-provided hash for inbound integrity verification.

### 3. Metadata & File Management

Used primarily by the UI for management actions.

| Method  | Endpoint             | Description                                                                        |
|:--------|:---------------------|:-----------------------------------------------------------------------------------|
| `GET`   | `/_/api/v1/fs/*path` | Returns **JSON** listing (if dir) or **JSON** metadata (if file).                  |
| `PATCH` | `/_/api/v1/fs/*path` | **Update Metadata**. Change tags, expiry, or immutability.                         |
| `POST`  | `/_/api/v1/fs/*path` | **System Actions**. Body: `{"create": "directory"}` or `{"rename_to": "new.txt"}`. |

### 4. Streams & Discovery

Query the logical hierarchy of your artifacts.

| Method | Endpoint                  | Description                                                |
|:-------|:--------------------------|:-----------------------------------------------------------|
| `GET`  | `/_/api/v1/streams`       | Returns a list of all unique stream names.                 |
| `GET`  | `/_/api/v1/streams/:name` | Returns all groups and nested files for a specific stream. |

### 5. Search & System

| Method | Endpoint                 | Description                                            |
|:-------|:-------------------------|:-------------------------------------------------------|
| `GET`  | `/_/api/v1/search?q=...` | Global search across paths, tags, and streams.         |
| `GET`  | `/_/api/v1/settings`     | Returns version, build info, and active configuration. |
| `POST` | `/_/api/v1/system/sync`  | Manually triggers a filesystem-to-database re-scan.    |

### 6. Administrative Management

| Method   | Endpoint                  | Description                                 |
|:---------|:--------------------------|:--------------------------------------------|
| `POST`   | `/_/api/login`            | Human login. Returns JWT.                   |
| `GET`    | `/_/api/auth/me`          | Info on current user.                       |
| `GET`    | `/_/api/admin/users`      | List all system users.                      |
| `PATCH`  | `/_/api/admin/users/:id`  | Reset user password or change Admin status. |
| `POST`   | `/_/api/admin/tokens`     | Generate a new scoped API Token.            |
| `DELETE` | `/_/api/admin/tokens/:id` | Revoke an API Token.                        |

## Configuration

Priority: **Defaults < YAML < Env < CLI Flags**

| YAML Key                  | Env Var        | CLI Flag      | Default          |                                               |
|:--------------------------|:---------------|:--------------|:-----------------|-----------------------------------------------|
| `server.port`             | `AF_PORT`      | `--port`      | `8080`           |                                               |
| `server.jwtsecret`        | `-`            | `-`           | ``               | Just some bytes (key) used for JWT generation |
| `database.file`           | `AF_DB_FILE`   | `--db`        | `artifactory.db` |                                               |
| `storage.base_dir`        | `AF_BASE_DIR`  | `--dir`       | `storage`        |                                               |
| `storage.max_upload_size` | `AF_MAX_SIZE`  | `--max-size`  | `100MB`          |                                               |
| `audit.file`              | `AF_AUDIT_LOG` | `--audit-log` | `audit.log`      |                                               |
