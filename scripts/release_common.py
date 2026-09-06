"""Shared identity validation for the release source and OCI tools."""

import re


def release_version(tag):
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", tag) or len(tag) > 128:
        raise ValueError("Expected a Docker-compatible version tag, such as v0.1.0.")
    return tag[1:]


def source_revision(revision):
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("Expected an exact source commit.")
    return revision
