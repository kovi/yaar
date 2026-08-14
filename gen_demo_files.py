#!/usr/bin/env python3
"""Generate a demo directory tree with realistic-looking files for Artifactory screenshots."""

import hashlib
import os
import random
import string
import struct
from pathlib import Path

SEED = 42
random.seed(SEED)

BASE_DIR = Path("artifactory-demo")

STRUCTURE = {
    "libs-release-local": {
        "com/acme/core": {
            "1.0.0": ["core-1.0.0.jar", "core-1.0.0.pom", "core-1.0.0-sources.jar"],
            "1.1.0": ["core-1.1.0.jar", "core-1.1.0.pom", "core-1.1.0-javadoc.jar"],
            "2.0.0": ["core-2.0.0.jar", "core-2.0.0.pom"],
        },
        "com/acme/api": {
            "3.2.1": ["api-3.2.1.jar", "api-3.2.1.pom", "api-3.2.1-sources.jar"],
            "3.3.0": ["api-3.3.0.jar", "api-3.3.0.pom"],
        },
        "com/acme/utils": {
            "0.9.5": ["utils-0.9.5.jar", "utils-0.9.5.pom"],
        },
        "org/thirdparty/logging": {
            "2.1.3": ["logging-2.1.3.jar", "logging-2.1.3.pom"],
        },
    },
    "docker-local": {
        "myapp": {
            "latest": ["manifest.json", "layer.tar.gz", "config.json"],
            "1.0": ["manifest.json", "layer.tar.gz", "config.json"],
            "1.1": ["manifest.json", "layer.tar.gz", "config.json"],
        },
        "baseimage": {
            "ubuntu-22.04": ["manifest.json", "layer.tar.gz"],
        },
    },
    "pypi-local": {
        "acme-sdk": {
            "1.0.0": ["acme_sdk-1.0.0-py3-none-any.whl", "acme-sdk-1.0.0.tar.gz"],
            "1.2.0": [
                "acme_sdk-1.2.0-py3-none-any.whl",
                "acme-sdk-1.2.0.tar.gz",
                "acme_sdk-1.2.0.dist-info",
            ],
        },
        "data-pipeline": {
            "0.3.1": ["data_pipeline-0.3.1-py3-none-any.whl"],
        },
    },
    "npm-local": {
        "@acme": {
            "ui-components": ["ui-components-2.1.0.tgz", "ui-components-2.2.0.tgz"],
            "api-client": ["api-client-1.0.0.tgz"],
        },
        "shared-utils": ["shared-utils-0.5.0.tgz", "shared-utils-0.6.1.tgz"],
    },
    "generic-local": {
        "releases": {
            "2024-Q1": [
                "installer-win64.exe",
                "installer-linux.sh",
                "installer-macos.dmg",
                "checksums.sha256",
            ],
            "2024-Q2": [
                "installer-win64.exe",
                "installer-linux.sh",
                "release-notes.txt",
            ],
            "2024-Q3": [
                "installer-win64.exe",
                "installer-linux.sh",
                "installer-macos.dmg",
            ],
        },
        "configs": [
            "app-config.yaml",
            "db-config.yaml",
            "logging-config.xml",
            "feature-flags.json",
        ],
        "docs": ["architecture.pdf", "api-reference.pdf", "changelog.txt", "README.md"],
        "scripts": ["deploy.sh", "backup.sh", "migrate.py", "health-check.sh"],
        "certs": ["server.crt", "ca-bundle.crt", "client.p12"],
    },
    "helm-local": {
        "myapp": {
            "0.1.0": ["myapp-0.1.0.tgz"],
            "0.2.0": ["myapp-0.2.0.tgz"],
            "1.0.0": ["myapp-1.0.0.tgz", "provenance.prov"],
        },
        "monitoring": {
            "3.5.1": ["monitoring-3.5.1.tgz"],
        },
    },
}

# Target size ranges per extension (min_bytes, max_bytes)
SIZE_HINTS = {
    ".jar": (200_000, 3_000_000),
    ".tar.gz": (500_000, 2_500_000),
    ".tgz": (300_000, 1_500_000),
    ".whl": (100_000, 800_000),
    ".dmg": (1_500_000, 3_000_000),
    ".exe": (1_000_000, 3_000_000),
    ".zip": (400_000, 2_000_000),
    ".pdf": (200_000, 1_200_000),
    ".p12": (3_000, 10_000),
    ".crt": (1_000, 4_000),
    ".prov": (500, 2_000),
    "default": (30_000, 500_000),
}


def size_for(filename: str) -> int:
    name = filename.lower()
    for ext, (lo, hi) in SIZE_HINTS.items():
        if name.endswith(ext):
            return random.randint(lo, hi)
    return random.randint(*SIZE_HINTS["default"])


def fake_text(size: int) -> bytes:
    words = [
        "acme",
        "release",
        "build",
        "artifact",
        "version",
        "snapshot",
        "dependency",
        "library",
        "package",
        "module",
        "service",
        "deploy",
        "pipeline",
        "config",
        "schema",
        "client",
        "server",
        "core",
        "api",
        "util",
        "auth",
        "data",
        "cache",
        "queue",
        "event",
        "stream",
    ]
    buf = []
    while sum(len(b) for b in buf) < size:
        line = " ".join(random.choices(words, k=random.randint(5, 12))) + "\n"
        buf.append(line.encode())
    return b"".join(buf)[:size]


def fake_binary(size: int) -> bytes:
    # Deterministic-ish binary blob seeded by content length
    r = random.Random(size ^ SEED)
    chunk = bytes(r.getrandbits(8) for _ in range(min(size, 4096)))
    repeats = (size // len(chunk)) + 1
    return (chunk * repeats)[:size]


TEXT_EXTS = {
    ".pom",
    ".xml",
    ".yaml",
    ".yml",
    ".json",
    ".txt",
    ".md",
    ".sh",
    ".py",
    ".toml",
    ".cfg",
    ".properties",
    ".dist-info",
}


def write_file(path: Path, filename: str):
    size = size_for(filename)
    ext = "." + filename.rsplit(".", 1)[-1] if "." in filename else ""
    content = fake_text(size) if ext in TEXT_EXTS else fake_binary(size)
    path.write_bytes(content)
    kb = size / 1024
    print(f"  {path.relative_to(BASE_DIR)}  ({kb:.0f} KB)")


def walk_structure(node, current_path: Path):
    if isinstance(node, list):
        for filename in node:
            write_file(current_path / filename, filename)
    elif isinstance(node, dict):
        for name, child in node.items():
            sub = current_path / name
            sub.mkdir(parents=True, exist_ok=True)
            walk_structure(child, sub)


def main():
    BASE_DIR.mkdir(exist_ok=True)
    print(f"Generating demo tree under '{BASE_DIR}/' (seed={SEED})\n")
    walk_structure(STRUCTURE, BASE_DIR)

    total = sum(f.stat().st_size for f in BASE_DIR.rglob("*") if f.is_file())
    file_count = sum(1 for f in BASE_DIR.rglob("*") if f.is_file())
    dir_count = sum(1 for d in BASE_DIR.rglob("*") if d.is_dir())
    print(
        f"\nDone: {file_count} files, {dir_count} directories, {total / 1024 / 1024:.1f} MB total"
    )


if __name__ == "__main__":
    main()