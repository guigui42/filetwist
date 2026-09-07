#!/usr/bin/env python3

import re
import sys


STABLE_VERSION = re.compile(r"^v([0-9]+)\.([0-9]+)\.([0-9]+)$")


def main() -> int:
    if len(sys.argv) != 2 or sys.argv[1] not in {"patch", "minor", "major"}:
        print("usage: next-release-version.py patch|minor|major", file=sys.stderr)
        return 2

    versions = []
    for line in sys.stdin:
        match = STABLE_VERSION.fullmatch(line.strip())
        if match is not None:
            versions.append(tuple(int(part) for part in match.groups()))

    major, minor, patch = max(versions, default=(0, 0, 0))
    match sys.argv[1]:
        case "patch":
            patch += 1
        case "minor":
            minor += 1
            patch = 0
        case "major":
            major += 1
            minor = 0
            patch = 0

    print(f"v{major}.{minor}.{patch}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

