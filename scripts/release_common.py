"""Shared identity validation for the release source and OCI tools."""

import re


def release_identity(tag):
    match = re.fullmatch(r"(media-)?v([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?)", tag)
    if not match or len(tag) > 128:
        raise ValueError("Expected a Docker-compatible version tag, such as v0.1.0 or media-v1.0.0.")
    component = "media" if match[1] else "app"
    version = match[2]
    prefix = f"filetwist_{'media_' if component == 'media' else ''}{version}"
    return {
        "component": component,
        "version": version,
        "recipe": "deploy/Dockerfile.runtime" if component == "media" else "deploy/Dockerfile",
        "asset_prefix": prefix,
        "source_manifest": prefix + "_sources.json",
    }


def release_version(tag):
    return release_identity(tag)["version"]


def asset_prefix(tag):
    return release_identity(tag)["asset_prefix"]


def source_revision(revision):
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("Expected an exact source commit.")
    return revision
