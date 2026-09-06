#!/usr/bin/env python3
"""Prepare exact, reviewed corresponding source. Never approve a dependency implicitly."""

import argparse
import concurrent.futures
import csv
import gzip
import hashlib
import io
import importlib.util
import json
import os
from pathlib import Path, PurePosixPath
import re
import shlex
import subprocess
import tarfile
from urllib.parse import quote, unquote, urlsplit, urlunsplit

from release_common import release_version as version, source_revision


LICENSES = "usr/local/share/licenses/filetwist/"
VIPS = "opt/vips/share/licenses/libvips/"
FFMPEG = "opt/ffmpeg/share/licenses/ffmpeg/"
DPKG_STATUS = "var/lib/dpkg/status"
FIELDS = ["Binary package", "Binary version", "Source package", "Source version"]
GO_MANIFESTS = {"go.mod", "go.sum", "go.work", "go.work.sum"}
VENDOR_DIRECTORIES = {"vendor", "third_party", "third-party", "thirdparty"}
STATIC = "internal/web/static/"
KNOWN_THIRD_PARTY = {STATIC + "htmx.min.js", STATIC + "htmx.LICENSE"}


def run(*args, **kwargs):
    return subprocess.run(args, check=True, stdout=subprocess.PIPE, **kwargs).stdout


def sha256(data):
    if isinstance(data, Path):
        with data.open("rb") as stream:
            return hashlib.file_digest(stream, "sha256").hexdigest()
    return hashlib.sha256(data).hexdigest()


def repository(value):
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", value):
        raise ValueError("Expected owner/repository.")
    return value


def release_metadata(repo, tag, revision):
    repository(repo)
    source_revision(revision)
    return {
        "sha": revision,
        "version": version(tag) if tag else revision,
        "image": f"ghcr.io/{repo.lower()}" if tag else "filetwist",
        "tag": tag or "v0.0.0-ci",
        "source_url": f"https://github.com/{repo}/releases/tag/{tag}/" if tag else "",
    }


def output_values(values):
    content = "".join(f"{key}={value}\n" for key, value in values.items())
    if os.environ.get("GITHUB_OUTPUT"):
        with open(os.environ["GITHUB_OUTPUT"], "a") as stream:
            stream.write(content)
    else:
        print(content, end="")


def safe_name(name):
    path = PurePosixPath(name)
    if (path.is_absolute() or "\\" in name or re.search(r"[\x00-\x1f\x7f]", name)
            or any(part in ("", ".", "..") for part in name.split("/"))):
        raise ValueError(f"Unsafe archive path: {name}")
    return name


def reject_symlinks(path):
    if any(item.is_symlink() for item in (path, *path.parents)):
        raise ValueError(f"Refusing output symlink: {path}")


def read_archive(archive):
    files = {}
    for member in archive:
        name = safe_name(member.name.removesuffix("/"))
        if member.isdir():
            continue
        if not member.isfile() or name in files:
            raise ValueError(f"Non-regular or duplicate archive member: {name}")
        files[name] = archive.extractfile(member).read()
    return files


def archive_bytes(entries):
    output = io.BytesIO()
    with gzip.GzipFile(fileobj=output, mode="wb", filename="", mtime=0, compresslevel=6) as zipped:
        with tarfile.open(fileobj=zipped, mode="w") as archive:
            for name, content in sorted(entries.items()):
                info = tarfile.TarInfo(safe_name(name))
                info.size, info.mode = len(content), 0o644
                archive.addfile(info, io.BytesIO(content))
    return output.getvalue()


def dependency_inputs(application_archive, first_party_assets, additional_paths):
    if not isinstance(first_party_assets, list) or not isinstance(additional_paths, list):
        raise ValueError("Dependency review must declare first_party_assets and additional_dependency_paths lists.")
    for path in [*first_party_assets, *additional_paths]:
        if not isinstance(path, str):
            raise ValueError("Dependency input paths must be strings.")
        safe_name(path)
    if len(set(first_party_assets)) != len(first_party_assets) or len(set(additional_paths)) != len(additional_paths):
        raise ValueError("Duplicate dependency input declarations.")
    if any(not path.startswith(STATIC) or path in KNOWN_THIRD_PARTY
           or VENDOR_DIRECTORIES.intersection(PurePosixPath(path).parts) for path in first_party_assets):
        raise ValueError("First-party exemptions cannot hide known third-party or vendored code.")
    with tarfile.open(fileobj=io.BytesIO(application_archive)) as archive:
        entries = {}
        for member in archive:
            name = safe_name(member.name.removesuffix("/"))
            if member.isdir():
                continue
            if name in entries:
                raise ValueError(f"Duplicate application source archive member: {name}")
            entries[name] = member
        if "go.mod" not in entries:
            raise ValueError("Application source archive has no go.mod.")
        if ".gitmodules" in entries:
            raise ValueError("Git submodules require explicit corresponding-source support before publication.")
        for path in first_party_assets:
            if path not in entries or not entries[path].isfile():
                raise ValueError(f"Missing or non-regular declared first-party asset: {path}")
        for path in additional_paths:
            if not any(name == path or name.startswith(path + "/") for name in entries):
                raise ValueError(f"Declared dependency input is absent: {path}")
        # Nulls bind absence too: adding a sum/workspace file or replacing an
        # expected vendored asset cannot silently reuse the previous review.
        result = {path: None for path in sorted(GO_MANIFESTS | KNOWN_THIRD_PARTY | {".gitmodules"})}
        for name, member in sorted(entries.items()):
            path = PurePosixPath(name)
            selected = (
                path.name in GO_MANIFESTS or name in result
                or bool(VENDOR_DIRECTORIES.intersection(path.parts))
                or (name.startswith(STATIC) and name not in first_party_assets)
                or any(name == extra or name.startswith(extra + "/") for extra in additional_paths)
            )
            if not selected:
                continue
            if not member.isfile():
                raise ValueError(f"Dependency input is not a regular file: {name}")
            with archive.extractfile(member) as stream:
                result[name] = hashlib.file_digest(stream, "sha256").hexdigest()
    return dict(sorted(result.items()))


def check_dependency_inputs(application_archive, policy):
    observed = dependency_inputs(application_archive, policy.get("first_party_assets"),
                                 policy.get("additional_dependency_paths", []))
    expected = policy.get("dependency_inputs_sha256")
    if not isinstance(expected, dict) or observed != expected:
        changed = sorted(name for name in set(observed) | set(expected or {})
                         if name not in observed or name not in (expected or {})
                         or observed[name] != expected[name])
        raise ValueError("Dependency manifests or vendored runtime assets changed. Renew review: " + ", ".join(changed))
    return observed


def parse_inventory(content):
    reader = csv.DictReader(io.StringIO(content.decode()), delimiter="\t")
    if reader.fieldnames != FIELDS:
        raise ValueError("Unexpected candidate package inventory schema.")
    packages, binaries = {}, set()
    for row in reader:
        name, release = row["Source package"], row["Source version"]
        binary, binary_version = row["Binary package"], row["Binary version"]
        if not re.fullmatch(r"[a-z0-9][a-z0-9+.-]*", name):
            raise ValueError(f"Invalid Debian source name: {name}")
        if not re.fullmatch(r"[a-z0-9][a-z0-9+.-]*(?::[a-z0-9]+)?", binary):
            raise ValueError(f"Invalid binary name: {binary}")
        for value in (release, binary_version):
            if not re.fullmatch(r"[0-9][A-Za-z0-9.+:~\-]*", value):
                raise ValueError(f"Invalid Debian version: {value}")
        if binary in binaries:
            raise ValueError(f"Duplicate binary package: {binary}")
        binaries.add(binary)
        package = packages.setdefault((name, release), {"name": name, "version": release, "binary_packages": []})
        package["binary_packages"].append({"name": binary, "version": binary_version})
    if not packages:
        raise ValueError("Empty package inventory.")
    for package in packages.values():
        package["binary_packages"].sort(key=lambda item: item["name"])
    return [packages[name] for name in sorted(packages)]


def source_key(package):
    return package["name"], package["version"]


def parse_control(content):
    records, record, field = [], {}, None
    for line in content.decode().splitlines() + [""]:
        if not line:
            if record:
                records.append(record)
            record, field = {}, None
        elif line[0].isspace():
            if field is None:
                raise ValueError("Unexpected dpkg control continuation.")
            record[field] += " " + line.strip()
        else:
            field, separator, value = line.partition(":")
            if not separator or field in record or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9-]*", field):
                raise ValueError("Invalid or duplicate dpkg control field.")
            record[field] = value.strip()
    return records


def add_built_using(packages, content):
    installed = {}
    for record in parse_control(content):
        if record.get("Status", "").split()[-1:] != ["installed"]:
            continue
        key = record["Package"], record["Architecture"]
        if key in installed:
            raise ValueError(f"Duplicate installed dpkg record: {key}")
        installed[key] = record
    by_key = {source_key(package): package for package in packages}
    matched = set()
    for package in list(packages):
        for binary in package["binary_packages"]:
            name, _, architecture = binary["name"].partition(":")
            matches = [(key, record) for key, record in installed.items()
                       if key[0] == name and (not architecture or key[1] == architecture)]
            if len(matches) != 1 or matches[0][1]["Version"] != binary["version"]:
                raise ValueError(f"dpkg status differs from binary inventory: {binary['name']}")
            key, record = matches[0]
            source = re.fullmatch(
                r"([a-z0-9][a-z0-9+.-]*)(?:\s+\(([0-9][A-Za-z0-9.+:~\-]*)\))?",
                record.get("Source", record["Package"]),
            )
            if not source or (source[1], source[2] or record["Version"]) != source_key(package):
                raise ValueError(f"dpkg source identity differs from binary inventory: {binary['name']}")
            matched.add(key)
            for field in ("Built-Using", "Static-Built-Using"):
                if not record.get(field):
                    continue
                seen = set()
                for relation in record[field].split(","):
                    match = re.fullmatch(
                        r"\s*([a-z0-9][a-z0-9+.-]*)\s*\(\s*=\s*([0-9][A-Za-z0-9.+:~\-]*)\s*\)\s*",
                        relation,
                    )
                    if not match:
                        raise ValueError(f"Non-exact {field} relation in {binary['name']}: {relation}")
                    identity = match[1], match[2]
                    if identity in seen:
                        raise ValueError(f"Duplicate {field} source pair: {identity}")
                    seen.add(identity)
                    source = by_key.setdefault(identity, {
                        "name": identity[0], "version": identity[1], "binary_packages": [],
                    })
                    source.setdefault("built_using", []).append({
                        "binary_package": binary["name"], "binary_version": binary["version"],
                        "field": field, "notice_sha256": binary["notice_sha256"],
                    })
    if matched != set(installed):
        raise ValueError("dpkg status contains installed binaries missing from the image inventory.")
    for package in by_key.values():
        if "built_using" in package:
            package["built_using"].sort(key=lambda item: (item["binary_package"], item["field"]))
    return [by_key[key] for key in sorted(by_key)]


def candidate_sources(files):
    packages = parse_inventory(files[LICENSES + "debian-packages.tsv"])
    for package in packages:
        for binary in package["binary_packages"]:
            notice = f"usr/share/doc/{binary['name'].split(':')[0]}/copyright"
            if notice not in files:
                raise ValueError(f"Missing installed copyright notice: {notice}")
            binary["notice_sha256"] = sha256(files[notice])
    if DPKG_STATUS not in files:
        raise ValueError("Missing dpkg status: Built-Using/Static-Built-Using source closure cannot be checked.")
    packages = add_built_using(packages, files[DPKG_STATUS])
    snapshot = files[LICENSES + "debian-snapshot.txt"].decode().strip()
    custom = custom_ffmpeg(files, snapshot)
    if custom:
        if any(package["name"] == "ffmpeg" for package in packages):
            raise ValueError("Candidate includes both custom and packaged/embedded FFmpeg. Review the mixed stack.")
        packages.append(custom)
        packages.sort(key=source_key)
    return packages, custom, snapshot


def source_path(package, item):
    return f"debian/{package['name']}/{quote(package['version'], safe='')}/{item['name']}"


def attach_uris(packages, text, snapshot):
    if not re.fullmatch(r"[0-9]{8}T[0-9]{6}Z", snapshot):
        raise ValueError("Invalid Debian snapshot.")
    by_key = {source_key(package): package for package in packages}
    for package in packages:
        package["source_files"] = []
    seen, current = set(), None
    for line in text.splitlines():
        if line.startswith("FILETWIST-SOURCE\t"):
            _, name, release = line.split("\t")
            current = name, release
            if current not in by_key:
                raise ValueError(f"Unexpected requested source: {current}")
            continue
        if not line.startswith("'"):
            continue
        url, filename, size, checksum = shlex.split(line)
        parsed = urlsplit(url)
        path = unquote(parsed.path)
        if (parsed.hostname != "snapshot.debian.org" or parsed.netloc != parsed.hostname
                or parsed.scheme not in ("http", "https") or parsed.query or parsed.fragment
                or not re.fullmatch(
                    rf"/archive/debian(?:-security)?/{snapshot}/pool/(?:updates/)?[^/]+/[^/]+/[^/]+/[^/]+", path
                )):
            raise ValueError(f"Source URL is not from the candidate's pinned snapshot: {url}")
        name = path.split("/")[-2]
        safe_name(filename)
        if "/" in filename or path.split("/")[-1] != filename:
            raise ValueError(f"Unexpected source file: {filename}")
        candidates = [key for key in by_key if key[0] == name]
        identity = current or (candidates[0] if len(candidates) == 1 else None)
        if identity not in by_key or identity[0] != name:
            raise ValueError(f"Unscoped or ambiguous source version for {filename}")
        if not re.fullmatch(r"SHA256:[0-9a-f]{64}", checksum) or not size.isdigit() or int(size) <= 0:
            raise ValueError(f"Missing authenticated SHA-256/size: {filename}")
        if (identity, filename) in seen:
            raise ValueError(f"Duplicate source file: {filename}")
        seen.add((identity, filename))
        by_key[identity]["source_files"].append({
            "name": filename, "url": urlunsplit(parsed._replace(scheme="https")),
            "size": int(size), "sha256": checksum.removeprefix("SHA256:"),
        })
    for package in packages:
        expected = f"{package['name']}_{package['version'].split(':', 1)[-1]}.dsc"
        descriptors = [item["name"] for item in package["source_files"] if item["name"].endswith(".dsc")]
        if descriptors != [expected] or len(package["source_files"]) < 2:
            raise ValueError(f"Missing or mismatched exact source set: {package['name']}")
        package["source_files"].sort(key=lambda item: item["name"])


def apply_policy(packages, policy):
    if policy.get("schema_version") != 1:
        raise ValueError("Unsupported source review schema.")
    reviews = {source_key(item): item for item in policy["debian_sources"]}
    actual = {source_key(package) for package in packages}
    if len(reviews) != len(policy["debian_sources"]) or set(reviews) != actual:
        missing = ", ".join(f"{name}={release}" for name, release in sorted(actual - set(reviews)))
        raise ValueError("Dependency/source-closure review changed. Renew the review. Unreviewed: " + missing)
    for package in packages:
        review = reviews[source_key(package)]
        if (review.get("version") != package["version"]
                or review.get("binary_packages") != package["binary_packages"]
                or review.get("built_using", []) != package.get("built_using", [])
                or review.get("source_route") not in ("mirror", "upstream")
                or not review.get("reason")):
            raise ValueError(f"Unreviewed dependency identity, notice, or source route: {package['name']}")
        package["source_route"] = review["source_route"]
        package["reason"] = review["reason"]
        package["references"] = review.get("references", [])


def require_publication_clearance(policy):
    if policy.get("status") != "approved" or not isinstance(policy.get("blockers"), list):
        raise ValueError("Source routing is not publication clearance. The reviewed policy must explicitly be approved.")
    if any(not isinstance(item, dict) for item in policy["blockers"]):
        raise ValueError("Invalid redistribution blocker record.")
    unresolved = [str(item.get("id") or "unnamed") for item in policy["blockers"]
                  if item.get("status") != "resolved"
                  or not isinstance(item.get("resolution"), str) or not item["resolution"].strip()]
    if unresolved:
        raise ValueError("Unresolved redistribution/source/notice blockers: " + ", ".join(unresolved))


def custom_ffmpeg(files, snapshot):
    if FFMPEG + "SOURCE" not in files:
        return None
    fields = {}
    for line in files[FFMPEG + "SOURCE"].decode().splitlines():
        if ": " in line:
            key, value = line.split(": ", 1)
            if key in fields:
                raise ValueError("Duplicate custom FFmpeg source field.")
            fields[key] = value
    if (fields.get("Source") != "ffmpeg" or fields.get("Snapshot") != snapshot
            or not re.fullmatch(r"[0-9][A-Za-z0-9.+:~\-]*", fields.get("Version", ""))
            or not re.fullmatch(r"[0-9a-f]{64}", fields.get("SHA256", ""))):
        raise ValueError("Invalid custom FFmpeg source identity.")
    descriptor = f"ffmpeg_{fields['Version'].split(':', 1)[-1]}.dsc"
    if fields.get("Descriptor") != descriptor or sha256(files[FFMPEG + descriptor]) != fields["SHA256"]:
        raise ValueError("Custom FFmpeg descriptor differs from its recorded identity.")
    for name in ("copyright", "configure-flags.txt", "build-ffmpeg.sh", "SHA256SUMS"):
        if not files.get(FFMPEG + name):
            raise ValueError(f"Missing custom FFmpeg build material: {name}")
    return {
        "name": "ffmpeg", "version": fields["Version"], "binary_packages": [],
        "custom_build": "ffmpeg",
        "build_material_sha256": {name: sha256(content) for name, content in sorted(files.items())
                                  if name.startswith(FFMPEG)},
    }


def apply_custom_policy(custom, policy, files):
    expected = {"ffmpeg"} if custom else set()
    if set(policy.get("custom_builds", {})) != expected:
        raise ValueError("Custom-built dependency set changed. Renew its source review.")
    if not custom:
        return
    review = policy["custom_builds"]["ffmpeg"]
    if (review.get("version") != custom["version"]
            or review.get("build_material_sha256") != custom["build_material_sha256"]
            or review.get("source_route") not in ("mirror", "upstream") or not review.get("reason")):
        raise ValueError("Unreviewed custom FFmpeg identity, configuration, notices or source route.")
    checksums = {}
    for line in files[FFMPEG + "SHA256SUMS"].decode().splitlines():
        value, name = line.split("  ", 1)
        safe_name(name)
        if name in checksums:
            raise ValueError("Duplicate custom FFmpeg source checksum.")
        checksums[name] = value
    if checksums != {item["name"]: item["sha256"] for item in custom["source_files"]}:
        raise ValueError("Custom FFmpeg source checksums differ from authenticated APT metadata.")
    custom.update(source_route=review["source_route"], reason=review["reason"],
                  references=review.get("references", []))


def release_assets(repo, tag):
    release = json.loads(run("gh", "api", f"repos/{repo}/releases/tags/{tag}"))
    if release["draft"]:
        raise ValueError("Publish the binary GitHub release first.")
    pages = json.loads(run("gh", "api", "--paginate", "--slurp",
                          f"repos/{repo}/releases/{release['id']}/assets?per_page=100"))
    return [item for page in pages for item in page]


def asset_bytes(repo, asset):
    return run("gh", "api", "-H", "Accept: application/octet-stream",
               f"repos/{repo}/releases/assets/{asset['id']}")


def upload_asset(repo, tag, name, content):
    # gh never replaces an asset unless --clobber is supplied. Do not use it.
    directory = Path(".release/source-upload")
    reject_symlinks(directory)
    directory.mkdir(parents=True, exist_ok=True)
    target = directory / safe_name(name)
    reject_symlinks(target)
    if "/" in name:
        raise ValueError(f"Asset name must be a basename: {name}")
    if isinstance(content, Path):
        run("gh", "release", "upload", tag, f"{content}#{name}", "--repo", repo)
    else:
        target.write_bytes(content)
        try:
            run("gh", "release", "upload", tag, str(target), "--repo", repo)
        finally:
            target.unlink()


def publish_assets(repo, tag, assets):
    existing = {item["name"]: item for item in release_assets(repo, tag)}
    # Validate ALL existing assets before writing any missing ones.
    for name, content in assets.items():
        if name in existing and sha256(asset_bytes(repo, existing[name])) != sha256(content):
            raise ValueError(f"Existing release asset differs: {name}. Never overwrite it or move the tag.")
    for name in sorted(assets, key=lambda value: (value.endswith("_sources.json"), value)):
        if name not in existing:
            upload_asset(repo, tag, name, assets[name])
    verify_assets(repo, tag, assets)


def verify_assets(repo, tag, assets):
    verified = {item["name"]: item for item in release_assets(repo, tag)}
    for name, content in assets.items():
        if name not in verified or sha256(asset_bytes(repo, verified[name])) != sha256(content):
            raise ValueError(f"Published asset verification failed: {name}")


def fetch(item, destination):
    reject_symlinks(destination)
    if not destination.exists():
        destination.parent.mkdir(parents=True, exist_ok=True)
        partial = destination.with_suffix(destination.suffix + ".part")
        reject_symlinks(partial)
        try:
            run("curl", "--fail", "--location", "--silent", "--show-error",
                "--proto", "=https", "--proto-redir", "=https", "--retry", "3",
                "--connect-timeout", "15", "--max-time", "600", "--output", str(partial), item["url"])
            if sha256(partial) != item["sha256"]:
                raise ValueError(f"Downloaded source checksum mismatch: {item['url']}")
            partial.replace(destination)
        finally:
            partial.unlink(missing_ok=True)
    if sha256(destination) != item["sha256"] or (
        item.get("size") is not None and destination.stat().st_size != item["size"]
    ):
        raise ValueError(f"Cached source checksum/size mismatch: {destination}")


def check_link(url):
    run("curl", "--head", "--fail", "--location", "--silent", "--show-error",
        "--proto", "=https", "--proto-redir", "=https", "--retry", "3",
        "--connect-timeout", "15", "--max-time", "90", "--output", os.devnull, url)


def verify_descriptor(path, package):
    text = path.read_text()
    source = re.search(r"^Source: (\S+)$", text, re.M)
    release = re.search(r"^Version: (\S+)$", text, re.M)
    if not source or not release or (source[1], release[1]) != (package["name"], package["version"]):
        raise ValueError(f"Descriptor identity mismatch: {package['name']}")
    block = re.search(r"^Checksums-Sha256:\n((?: .+\n)+)", text, re.M)
    if not block:
        raise ValueError(f"Descriptor has no SHA-256 source list: {path}")
    listed = {}
    for line in block[1].splitlines():
        digest, size, name = line.split()
        if name in listed:
            raise ValueError(f"Duplicate descriptor entry: {name}")
        listed[name] = (digest, int(size))
    expected = {item["name"]: (item["sha256"], item["size"]) for item in package["source_files"]
                if not item["name"].endswith(".dsc")}
    if listed != expected:
        raise ValueError(f"APT source set differs from its descriptor: {package['name']}")


def mirror_archive(path, entries):
    reject_symlinks(path)
    with path.open("wb") as output:
        with gzip.GzipFile(fileobj=output, filename="", mode="wb", mtime=0, compresslevel=1) as zipped:
            with tarfile.open(fileobj=zipped, mode="w") as archive:
                for name, source in sorted(entries.items()):
                    info = tarfile.TarInfo(safe_name(name))
                    info.size, info.mode = source.stat().st_size, 0o644
                    with source.open("rb") as stream:
                        archive.addfile(info, stream)


def source_notices(packages, policy, cache):
    """Retain embedded-code notices and explicitly reviewed extra attributions."""
    notices = {}
    by_key = {source_key(package): package for package in packages}
    for package in packages:
        if not package.get("built_using") or package["source_route"] != "mirror":
            continue
        count = 0
        for item in package["source_files"]:
            if ".tar." not in item["name"]:
                continue
            with tarfile.open(cache / source_path(package, item)) as archive:
                for member in archive:
                    if not member.isfile() or not re.fullmatch(
                        r"(?:licen[cs]e|copying|notice|copyright)(?:[._-].*)?",
                        PurePosixPath(member.name).name, re.I,
                    ):
                        continue
                    name = member.name
                    while name.startswith("./"):
                        name = name[2:]
                    safe_name(name)
                    if member.size > 1024 * 1024:
                        raise ValueError(f"Unexpectedly large source notice: {name}")
                    destination = f"source/embedded-notices/{source_path(package, item)}/{name}"
                    if destination in notices:
                        raise ValueError(f"Duplicate source notice: {destination}")
                    notices[destination] = archive.extractfile(member).read()
                    count += 1
        if not count:
            raise ValueError(f"Missing accompanying embedded-code notices: {package['name']}")
    for extra in policy.get("supplemental_notices", []):
        identity = extra["source"], extra["version"]
        package = by_key[identity]
        matches = [item for item in package["source_files"] if item["name"] == extra["archive"]]
        if len(matches) != 1:
            raise ValueError(f"Unknown supplemental-notice source archive: {identity}")
        item = matches[0]
        path = cache / source_path(package, item)
        fetch(item, path)
        with tarfile.open(path) as archive:
            matches = [member for member in archive if member.name == extra["member"]]
            if len(matches) != 1 or not matches[0].isfile() or matches[0].size > 1024 * 1024:
                raise ValueError(f"Missing or invalid supplemental notice: {extra['member']}")
            content = archive.extractfile(matches[0]).read()
        if sha256(content) != extra["sha256"]:
            raise ValueError(f"Supplemental notice changed: {extra['member']}")
        destination = f"source/supplemental-notices/{safe_name(extra['source'])}/{safe_name(extra['member'])}"
        if destination in notices:
            raise ValueError(f"Duplicate supplemental notice: {destination}")
        notices[destination] = content
    return notices


def restore_mirrors(repo, tag, asset_name, expected, cache):
    assets = [item for item in release_assets(repo, tag) if item["name"] == asset_name]
    if not assets:
        return
    if len(assets) != 1:
        raise ValueError("Duplicate mirrored-source asset.")
    # Recovery works even if the original upstream host has disappeared. Every
    # member is checked against fresh authenticated APT metadata / reviewed libvips.
    path = cache / "existing-mirrors.tar.gz"
    reject_symlinks(path)
    cache.mkdir(parents=True, exist_ok=True)
    path.write_bytes(asset_bytes(repo, assets[0]))
    seen = set()
    valid = False
    try:
        with tarfile.open(path) as archive:
            for member in archive:
                name = safe_name(member.name)
                if not member.isfile() or name not in expected or name in seen:
                    raise ValueError(f"Unexpected published mirror archive member: {name}")
                seen.add(name)
                destination = cache / name
                destination.parent.mkdir(parents=True, exist_ok=True)
                reject_symlinks(destination)
                with archive.extractfile(member) as source, destination.open("wb") as target:
                    while chunk := source.read(1024 * 1024):
                        target.write(chunk)
                if sha256(destination) != expected[name]["sha256"]:
                    raise ValueError(f"Published source mirror differs: {name}")
        if seen != set(expected):
            raise ValueError("Published source mirror archive is incomplete.")
        valid = True
        return path
    finally:
        if not valid:
            path.unlink()


def previous_materials(repo, tag, prefix):
    assets = {item["name"]: item for item in release_assets(repo, tag)}
    if prefix + "_sources.json" not in assets:
        return None
    manifest = json.loads(asset_bytes(repo, assets[prefix + "_sources.json"]))
    name = prefix + "_source-materials.tar.gz"
    if name not in assets:
        raise ValueError("Existing source manifest has no build materials. Restore the original asset.")
    content = asset_bytes(repo, assets[name])
    if sha256(content) != manifest["materials"]["sha256"]:
        raise ValueError("Published build materials differ from the source manifest.")
    with tarfile.open(fileobj=io.BytesIO(content)) as archive:
        files = read_archive(archive)
    if sha256(files["source/review.json"]) != manifest["review_sha256"]:
        raise ValueError("Published historical review differs from its recorded hash.")
    return content, files


def requests(args):
    with tarfile.open(args.inventory) as archive:
        files = read_archive(archive)
    packages, _, _ = candidate_sources(files)
    reject_symlinks(args.output)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text("".join(f"{package['name']}\t{package['version']}\n" for package in packages))
    print(f"Requested {len(packages)} exact source identities, including declared Built-Using/Static-Built-Using.")


def prepare(args):
    output = args.output
    reject_symlinks(output)
    output.mkdir(parents=True, exist_ok=True)
    prefix = f"filetwist_{version(args.tag)}"
    with tarfile.open(args.inventory) as archive:
        files = read_archive(archive)
    packages, custom, snapshot = candidate_sources(files)
    attach_uris(packages, args.uris.read_text(), snapshot)
    previous = previous_materials(args.repository, args.tag, prefix)
    policy_bytes = previous[1]["source/review.json"] if previous else args.policy.read_bytes()
    policy = json.loads(policy_bytes)
    apply_policy([package for package in packages if not package.get("custom_build")], policy)
    apply_custom_policy(custom, policy, files)
    fixed_notices = {name: sha256(content) for name, content in sorted(files.items())
                     if name.startswith("usr/share/common-licenses/") or name in (
                         LICENSES + "go.LICENSE", LICENSES + "htmx.LICENSE", VIPS + "LICENSE")}
    if policy.get("notice_sha256") != fixed_notices:
        raise ValueError("Toolchain, libvips, or common licence text changed. Renew the review.")
    vips_lines = files[VIPS + "SOURCE"].decode().splitlines()
    if len(vips_lines) < 3 or not re.fullmatch(r"SHA256: [0-9a-f]{64}", vips_lines[1]):
        raise ValueError("Missing exact libvips source identity.")
    match = re.fullmatch(
        r"https://github.com/libvips/libvips/releases/download/v([0-9.]+)/vips-\1.tar.xz", vips_lines[0]
    )
    if not match or "without source modifications" not in vips_lines[2]:
        raise ValueError("Custom or modified libvips sources need explicit tooling and review.")
    vips = {"version": match[1], "url": vips_lines[0], "sha256": vips_lines[1][8:],
            "notice_sha256": sha256(files[VIPS + "LICENSE"])}
    review = policy["libvips"]
    if (any(review.get(key) != value for key, value in vips.items())
            or review.get("source_route") not in ("upstream", "mirror") or not review.get("reason")):
        raise ValueError("Unreviewed libvips source or notice.")
    vips.update(source_route=review["source_route"], reason=review["reason"])

    revision = run("git", "-C", str(args.checkout), "rev-parse", "HEAD").decode().strip()
    if revision != args.revision or not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("Source checkout does not match the published binary release.")
    recipe = run("git", "-C", str(args.checkout), "show", "HEAD:deploy/Dockerfile")
    if recipe != files[LICENSES + "Dockerfile"]:
        raise ValueError("Candidate build recipe differs from its exact source commit.")
    # Review dependency build configuration too, not application version/revision labels.
    if sha256(recipe) != policy.get("dockerfile_sha256"):
        raise ValueError("Dockerfile changed. Renew dependency/build configuration review.")
    application_archive = run("git", "-C", str(args.checkout), "archive", "--format=tar", revision)
    dependency_hashes = check_dependency_inputs(application_archive, policy)
    source_url = f"https://github.com/{repository(args.repository)}/releases/tag/{args.tag}/"
    download_base = f"https://github.com/{args.repository}/releases/download/{args.tag}"
    cache = output / "cache"
    expected_mirrors = {
        source_path(package, item): item
        for package in packages if package["source_route"] == "mirror"
        for item in package["source_files"]
    }
    if vips["source_route"] == "mirror":
        expected_mirrors[f"libvips/vips-{vips['version']}.tar.xz"] = vips
    mirrors_name = prefix + "_source-mirrors.tar.gz"
    existing_mirrors = restore_mirrors(args.repository, args.tag, mirrors_name, expected_mirrors, cache)
    mirrored, links = {}, []
    for package in packages:
        for item in package["source_files"]:
            path = cache / source_path(package, item)
            if package["source_route"] == "mirror" or item["name"].endswith(".dsc"):
                fetch(item, path)
            if package["source_route"] == "mirror":
                mirrored[source_path(package, item)] = path
            else:
                links.append(item["url"])
            if item["name"].endswith(".dsc"):
                verify_descriptor(path, package)
    if vips["source_route"] == "mirror":
        path = cache / "libvips" / f"vips-{vips['version']}.tar.xz"
        fetch(vips, path)
        mirrored[f"libvips/{path.name}"] = path
    else:
        links.append(vips["url"])
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as executor:
        list(executor.map(check_link, links))

    materials = {name: content for name, content in files.items()
                 if name.startswith((LICENSES, VIPS, FFMPEG, "usr/share/common-licenses/"))
                 or name.endswith("/copyright") or name == DPKG_STATUS}
    materials["source/filetwist.tar"] = application_archive
    materials.update(source_notices(packages, policy, cache))
    materials["source/dependency-inputs.json"] = (json.dumps(dependency_hashes, indent=2) + "\n").encode()
    materials["source/review.json"] = policy_bytes
    materials_name = prefix + "_source-materials.tar.gz"
    if previous and previous[1] != materials:
        raise ValueError("Candidate notices/build materials differ from the immutable release assets.")
    (output / materials_name).write_bytes(previous[0] if previous else archive_bytes(materials))
    if existing_mirrors:
        reject_symlinks(output / mirrors_name)
        existing_mirrors.replace(output / mirrors_name)
    else:
        mirror_archive(output / mirrors_name, mirrored)
    spec = importlib.util.spec_from_file_location("container_image", Path(__file__).with_name("container-image.py"))
    image = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(image)
    candidate = image.OCI(args.candidate)
    try:
        runtime_digest = candidate.runtime["digest"]
        index_digest = candidate.root["digest"]
        labels = candidate.config["config"]["Labels"]
        if (labels.get("org.opencontainers.image.revision") != revision
                or labels.get("org.opencontainers.image.version") != version(args.tag)
                or labels.get("io.github.filetwist.corresponding-source") != source_url):
            raise ValueError("OCI source/revision labels differ from the prepared source.")
    finally:
        candidate.archive.close()
    manifest = {
        "schema_version": 1, "release": args.tag, "source_commit": revision,
        "platform": "linux/amd64", "debian_snapshot": snapshot,
        "runtime_manifest_sha256": runtime_digest,
        "image_index_sha256": index_digest,
        "inventory_sha256": sha256(files[LICENSES + "debian-packages.tsv"]),
        "dpkg_status_sha256": sha256(files[DPKG_STATUS]),
        "source_scope": ["installed", "Built-Using", "Static-Built-Using", "custom-builds"],
        "review_sha256": sha256(policy_bytes), "source_index_url": source_url,
        "dependency_inputs_sha256": dependency_hashes,
        "review_status": policy.get("status", "pending"),
        "blockers": policy.get("blockers", []),
        "materials": {"url": f"{download_base}/{materials_name}", "sha256": sha256(output / materials_name)},
        "mirrors": {"url": f"{download_base}/{mirrors_name}", "sha256": sha256(output / mirrors_name)},
        "libvips": vips, "debian_sources": packages,
    }
    (output / (prefix + "_sources.json")).write_text(json.dumps(manifest, indent=2) + "\n")
    rows = "\n".join(f"| {p['name']} | `{p['version']}` | {p['source_route']} |" for p in packages)
    (output / (prefix + "_sources.md")).write_text(
        f"# Corresponding source for Filetwist {args.tag}\n\n"
        f"Exact application commit: `{revision}`. Platform: Linux/amd64.\n\n"
        f"Review status: `{manifest['review_status']}`. Source provisioning alone is not "
        "clearance to publish the image; unresolved licence-combination, source or notice "
        "blockers must be resolved separately.\n\n"
        f"[Machine-readable inventory]({download_base}/{prefix}_sources.json) records exact "
        "Debian source sets, including declared Built-Using and Static-Built-Using contributions, "
        "authenticated APT SHA-256 hashes and original HTTPS locations.\n\n"
        f"[Mirrored source archives]({manifest['mirrors']['url']}) contain the complete selected "
        "Debian source sets (descriptors, upstream archives, patches and signatures) and "
        "libvips where the review requires mirroring. "
        f"[Notices and build materials]({manifest['materials']['url']}) include the exact "
        "application source, Dockerfile, dependency inventory, FFmpeg build options, "
        "copyright files, common licence texts and the review policy.\n\n"
        "Upstream routes refer to the exact original files listed in the JSON, not to a "
        "project homepage. The publisher remains responsible for continued equivalent "
        "source access. Report unavailable source links to the repository maintainer.\n\n"
        f"Custom libvips {vips['version']}: {vips['source_route']}. No source modifications.\n\n"
        "| Debian source | Exact version | Route |\n| --- | --- | --- |\n" + rows + "\n"
    )
    assets = sorted(output.glob(prefix + "_*.*"))
    checksums = "".join(f"{sha256(path)}  {path.name}\n" for path in assets
                        if not path.name.endswith("_source-checksums.txt"))
    (output / (prefix + "_source-checksums.txt")).write_text(checksums)
    print(f"Prepared {len(packages)} reviewed Debian source sets for {args.tag}.")


def resolve(args):
    version(args.tag)
    repository(args.repository)
    assets = release_assets(args.repository, args.tag)
    markers = [item for item in assets if item["name"] == "filetwist-source-commit.txt"]
    if len(markers) != 1:
        raise ValueError("Release must have exactly one filetwist-source-commit.txt asset.")
    revision = asset_bytes(args.repository, markers[0]).decode().strip()
    source_revision(revision)
    reference = json.loads(run("gh", "api", f"repos/{args.repository}/git/ref/tags/{args.tag}"))["object"]
    for _ in range(16):
        if reference["type"] != "tag":
            break
        reference = json.loads(run("gh", "api",
                                   f"repos/{args.repository}/git/tags/{reference['sha']}"))["object"]
    if reference["type"] != "commit" or reference["sha"] != revision:
        raise ValueError("Version tag no longer points at the commit recorded by the binary release.")
    previous = [item for item in assets if item["name"] == f"filetwist_{version(args.tag)}_sources.json"]
    runtime, image_index = "", ""
    if previous:
        manifest = json.loads(asset_bytes(args.repository, previous[0]))
        runtime = manifest.get("runtime_manifest_sha256", "")
        image_index = manifest.get("image_index_sha256", "")
        if (manifest.get("source_commit") != revision
                or not re.fullmatch(r"sha256:[0-9a-f]{64}", runtime)
                or not re.fullmatch(r"sha256:[0-9a-f]{64}", image_index)):
            raise ValueError("Existing source manifest is incompatible or differs from the release. Do not overwrite it.")
    output_values({**release_metadata(args.repository, args.tag, revision),
                   "runtime": runtime, "index": image_index})


def publish(args):
    resolve(args)
    prefix = f"filetwist_{version(args.tag)}"
    expected = {prefix + suffix for suffix in (
        "_sources.json", "_sources.md", "_source-materials.tar.gz",
        "_source-mirrors.tar.gz", "_source-checksums.txt",
    )}
    assets = {path.name: path for path in args.directory.iterdir() if path.is_file()}
    if set(assets) != expected:
        raise ValueError("Source artifact has missing or unexpected release assets.")
    checked = set()
    for line in assets[prefix + "_source-checksums.txt"].read_text().splitlines():
        digest, name = line.split("  ", 1)
        if name not in assets or name in checked or sha256(assets[name]) != digest:
            raise ValueError(f"Prepared source checksum mismatch: {name}")
        checked.add(name)
    if checked != expected - {prefix + "_source-checksums.txt"}:
        raise ValueError("Incomplete source checksums.")
    with tarfile.open(assets[prefix + "_source-materials.tar.gz"]) as archive:
        materials = read_archive(archive)
    require_publication_clearance(json.loads(materials["source/review.json"]))
    if args.command == "verify":
        verify_assets(repository(args.repository), args.tag, assets)
    else:
        publish_assets(repository(args.repository), args.tag, assets)
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as executor:
        list(executor.map(check_link, [
            f"https://github.com/{args.repository}/releases/download/{args.tag}/{name}"
            for name in sorted(assets)
        ]))


def check_links(args):
    name = f"filetwist_{version(args.tag)}_sources.json"
    assets = [item for item in release_assets(repository(args.repository), args.tag) if item["name"] == name]
    if len(assets) != 1:
        raise ValueError("Missing versioned source manifest.")
    manifest = json.loads(asset_bytes(args.repository, assets[0]))
    urls = [manifest["materials"]["url"], manifest["mirrors"]["url"]]
    urls += [item["url"] for package in manifest["debian_sources"]
             if package["source_route"] == "upstream" for item in package["source_files"]]
    if manifest["libvips"]["source_route"] == "upstream":
        urls.append(manifest["libvips"]["url"])
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as executor:
        list(executor.map(check_link, urls))
    print(f"All {len(urls)} public source URLs are reachable for {args.tag}.")


def inspect_inputs(args):
    source_revision(args.revision)
    archive = run("git", "-C", str(args.checkout), "archive", "--format=tar", args.revision)
    print(json.dumps(dependency_inputs(archive, args.first_party_asset, args.additional_path), indent=2))


def review_template(args):
    source_revision(args.revision)
    with tarfile.open(args.inventory) as archive:
        files = read_archive(archive)
    packages, custom, _ = candidate_sources(files)
    application = run("git", "-C", str(args.checkout), "archive", "--format=tar", args.revision)
    reason = "Pending review; provision matching source without approving publication."
    for package in packages:
        package.update(source_route="mirror", reason=reason, references=[])
    vips_lines = files[VIPS + "SOURCE"].decode().splitlines()
    if len(vips_lines) < 3:
        raise ValueError("Missing exact libvips source identity.")
    match = re.fullmatch(
        r"https://github.com/libvips/libvips/releases/download/v([0-9.]+)/vips-\1.tar.xz", vips_lines[0]
    )
    if not match or not re.fullmatch(r"SHA256: [0-9a-f]{64}", vips_lines[1]):
        raise ValueError("Missing exact libvips source identity.")
    policy = {
        "schema_version": 1, "status": "pending",
        "blockers": [{"id": "redistribution-review", "status": "pending", "resolution": ""}],
        "dockerfile_sha256": sha256(files[LICENSES + "Dockerfile"]),
        "first_party_assets": args.first_party_asset,
        "additional_dependency_paths": args.additional_path,
        "dependency_inputs_sha256": dependency_inputs(
            application, args.first_party_asset, args.additional_path,
        ),
        "notice_sha256": {name: sha256(content) for name, content in sorted(files.items())
                         if name.startswith("usr/share/common-licenses/") or name in (
                             LICENSES + "go.LICENSE", LICENSES + "htmx.LICENSE", VIPS + "LICENSE")},
        "libvips": {"version": match[1], "url": vips_lines[0], "sha256": vips_lines[1][8:],
                    "notice_sha256": sha256(files[VIPS + "LICENSE"]),
                    "source_route": "mirror", "reason": reason},
        "custom_builds": {"ffmpeg": custom} if custom else {},
        "debian_sources": [package for package in packages if not package.get("custom_build")],
        "supplemental_notices": [],
    }
    print(json.dumps(policy, indent=2))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    request_command = commands.add_parser("requests")
    request_command.add_argument("--inventory", type=Path, required=True)
    request_command.add_argument("--output", type=Path, required=True)
    for name in ("dependency-inputs", "review-template"):
        inputs_command = commands.add_parser(name)
        inputs_command.add_argument("--checkout", type=Path, required=True)
        inputs_command.add_argument("--revision", required=True)
        inputs_command.add_argument("--first-party-asset", action="append", default=[])
        inputs_command.add_argument("--additional-path", action="append", default=[])
        if name == "review-template":
            inputs_command.add_argument("--inventory", type=Path, required=True)
    for name in ("metadata", "resolve", "prepare", "publish", "verify", "check-links"):
        command = commands.add_parser(name)
        command.add_argument("--tag", required=name != "metadata", default="")
        command.add_argument("--repository", required=True)
        if name == "prepare":
            for value in ("inventory", "uris", "policy", "checkout", "output", "candidate"):
                command.add_argument("--" + value, type=Path, required=True)
            command.add_argument("--revision", required=True)
        elif name in ("publish", "verify"):
            command.add_argument("--directory", type=Path, required=True)
        elif name == "metadata":
            command.add_argument("--revision", required=True)
    args = parser.parse_args()
    {"metadata": lambda value: output_values(release_metadata(value.repository, value.tag, value.revision)),
     "dependency-inputs": inspect_inputs, "review-template": review_template,
     "requests": requests, "resolve": resolve, "prepare": prepare, "publish": publish,
     "verify": publish, "check-links": check_links}[args.command](args)


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, tarfile.TarError, subprocess.CalledProcessError) as error:
        raise SystemExit(f"ERROR: {error}") from error
