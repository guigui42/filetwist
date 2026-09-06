#!/usr/bin/env python3
"""Load and publish the same attested OCI archive, without rebuilding or replacing a tag."""

import argparse
import base64
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
from urllib.error import HTTPError
from urllib.parse import urlencode, urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener

from release_common import release_version, source_revision


INDEX = "application/vnd.oci.image.index.v1+json"
MANIFEST = "application/vnd.oci.image.manifest.v1+json"
ACCEPT = f"{INDEX}, {MANIFEST}, application/vnd.docker.distribution.manifest.list.v2+json"
VALIDATION_CHECKS = ["conversions", "size-budget", "browser"]


def digest(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def runtime_descriptor(index):
    descriptors = [item for item in index["manifests"]
                   if item.get("platform") == {"architecture": "amd64", "os": "linux"}]
    if len(descriptors) != 1:
        raise ValueError("Expected exactly one Linux/amd64 runtime manifest.")
    return descriptors[0]


def validation_receipt(candidate, path, revision, tag, run_id):
    source_revision(revision)
    labels = candidate.config["config"]["Labels"]
    if (not run_id
            or labels.get("org.opencontainers.image.revision") != revision
            or labels.get("org.opencontainers.image.version") != release_version(tag)):
        raise ValueError("Validated candidate does not match the release commit and version.")
    with path.open("rb") as stream:
        archive_hash = hashlib.file_digest(stream, "sha256").hexdigest()
    return {
        "schema_version": 1, "run_id": run_id, "source_commit": revision, "tag": tag,
        "archive_sha256": archive_hash, "image_index_sha256": candidate.root["digest"],
        "runtime_manifest_sha256": candidate.runtime["digest"], "checks": VALIDATION_CHECKS,
    }


def verify_validation(candidate, archive, receipt, revision, tag, run_id):
    expected = validation_receipt(candidate, archive, revision, tag, run_id)
    if receipt != expected:
        raise ValueError("CI validation receipt differs from the archive, release, run or required checks.")


class OCI:
    def __init__(self, path):
        self.archive = tarfile.open(path, "r:")
        try:
            self._read_index()
        except BaseException:
            self.archive.close()
            raise

    def _read_index(self):
        self.members = {}
        for member in self.archive:
            if member.isdir():
                continue
            if (not member.isfile() or not re.fullmatch(r"(index\.json|oci-layout|blobs/sha256/[0-9a-f]{64})",
                                                       member.name)
                    or member.name in self.members):
                raise ValueError(f"Unsafe or duplicate OCI archive member: {member.name}")
            self.members[member.name] = member
        with self.archive.extractfile(self.members["index.json"]) as stream:
            top = json.load(stream)
        if len(top["manifests"]) != 1:
            raise ValueError("Expected one root image descriptor.")
        self.root = top["manifests"][0]
        self.index = self.document(self.root)
        if self.root["mediaType"] != INDEX:
            raise ValueError("Candidate needs an OCI index containing provenance and SBOM.")
        self.runtime = runtime_descriptor(self.index)
        if self.runtime["mediaType"] != MANIFEST:
            raise ValueError("Runtime must be an OCI image manifest.")
        self.manifest = self.document(self.runtime)
        self.config = self.document(self.manifest["config"])
        if self.config.get("architecture") != "amd64" or self.config.get("os") != "linux":
            raise ValueError("Runtime configuration must be Linux/amd64.")
        self.validate_attestations()

    def stream(self, descriptor):
        value = descriptor["digest"]
        if not re.fullmatch(r"sha256:[0-9a-f]{64}", value):
            raise ValueError(f"Unsupported OCI digest: {value}")
        member = self.members["blobs/sha256/" + value[7:]]
        if member.size != descriptor["size"]:
            raise ValueError(f"OCI size mismatch: {value}")
        with self.archive.extractfile(member) as stream:
            if "sha256:" + hashlib.file_digest(stream, "sha256").hexdigest() != value:
                raise ValueError(f"OCI checksum mismatch: {value}")
        return self.archive.extractfile(member)

    def document(self, descriptor):
        with self.stream(descriptor) as stream:
            return json.load(stream)

    def validate_attestations(self):
        predicates = set()
        for descriptor in self.index["manifests"]:
            if descriptor == self.runtime:
                continue
            annotations = descriptor.get("annotations", {})
            if (descriptor.get("platform") != {"architecture": "unknown", "os": "unknown"}
                    or descriptor.get("mediaType") != MANIFEST
                    or annotations.get("vnd.docker.reference.type") != "attestation-manifest"
                    or annotations.get("vnd.docker.reference.digest") != self.runtime["digest"]):
                raise ValueError("Only attestations bound to the tested runtime may accompany it.")
            manifest = self.document(descriptor)
            config = self.document(manifest["config"])
            empty = manifest["config"]["mediaType"] == "application/vnd.oci.empty.v1+json" and config == {}
            legacy = (
                manifest["config"]["mediaType"] == "application/vnd.oci.image.config.v1+json"
                and config.get("architecture") == "unknown" and config.get("os") == "unknown"
                and not config.get("config", {}).get("Entrypoint")
                and not config.get("config", {}).get("Cmd")
            )
            if not (empty or legacy):
                raise ValueError("An attestation must not contain a runnable image configuration.")
            for layer in manifest["layers"]:
                if layer.get("mediaType") != "application/vnd.in-toto+json":
                    raise ValueError("Attestation layers must be in-toto statements.")
                statement = self.document(layer)
                subjects = statement.get("subject")
                if (statement.get("_type") not in (
                        "https://in-toto.io/Statement/v0.1", "https://in-toto.io/Statement/v1")
                        or not isinstance(subjects, list) or not subjects
                        or any(not isinstance(subject, dict)
                               or subject.get("digest", {}).get("sha256") != self.runtime["digest"][7:]
                               for subject in subjects)):
                    raise ValueError("Attestation statement subject differs from the tested runtime.")
                predicates.add(statement.get("predicateType"))
        if not {"https://spdx.dev/Document", "https://slsa.dev/provenance/v0.2"}.issubset(predicates) and not (
            "https://spdx.dev/Document" in predicates and "https://slsa.dev/provenance/v1" in predicates
        ):
            raise ValueError("Candidate must include both SBOM and build provenance.")

    def load(self, image, directory):
        directory.mkdir(parents=True, exist_ok=True)
        docker_tar = directory / "candidate-docker.tar"
        config_name = self.manifest["config"]["digest"][7:] + ".json"
        layers = []
        try:
            with tarfile.open(docker_tar, "w") as output:
                with self.stream(self.manifest["config"]) as stream:
                    info = tarfile.TarInfo(config_name)
                    info.size = self.manifest["config"]["size"]
                    output.addfile(info, stream)
                if len(self.manifest["layers"]) != len(self.config["rootfs"]["diff_ids"]):
                    raise ValueError("Layer count differs from the runtime config.")
                for number, descriptor in enumerate(self.manifest["layers"]):
                    layer_path = directory / "uncompressed-layer.tar"
                    with self.stream(descriptor) as source:
                        if descriptor["mediaType"].endswith("+gzip"):
                            source = gzip.GzipFile(fileobj=source)
                        elif descriptor["mediaType"] != "application/vnd.oci.image.layer.v1.tar":
                            raise ValueError("Unsupported OCI layer compression.")
                        with layer_path.open("wb") as target:
                            shutil.copyfileobj(source, target)
                    with layer_path.open("rb") as stream:
                        if "sha256:" + hashlib.file_digest(stream, "sha256").hexdigest() != (
                            self.config["rootfs"]["diff_ids"][number]
                        ):
                            raise ValueError("Uncompressed runtime layer checksum mismatch.")
                    name = f"{number}/layer.tar"
                    layers.append(name)
                    info = tarfile.TarInfo(name)
                    info.size = layer_path.stat().st_size
                    with layer_path.open("rb") as stream:
                        output.addfile(info, stream)
                    layer_path.unlink()
                metadata = json.dumps([{"Config": config_name, "RepoTags": [image], "Layers": layers}]).encode()
                info = tarfile.TarInfo("manifest.json")
                info.size = len(metadata)
                output.addfile(info, io.BytesIO(metadata))
            subprocess.run(["docker", "load", "--input", str(docker_tar)], check=True)
            actual = subprocess.check_output(["docker", "image", "inspect", "--format={{.Id}}", image]).decode().strip()
            if actual != self.manifest["config"]["digest"]:
                # New containerd-backed engines expose a manifest ID instead of a
                # config ID. Read back the exact config bytes rather than guessing.
                expected_name = "blobs/sha256/" + self.manifest["config"]["digest"][7:]
                found = False
                with subprocess.Popen(["docker", "save", image], stdout=subprocess.PIPE) as process:
                    try:
                        with tarfile.open(fileobj=process.stdout, mode="r|") as saved:
                            for member in saved:
                                if member.name in (expected_name, config_name):
                                    found = digest(saved.extractfile(member).read()) == self.manifest["config"]["digest"]
                        if process.wait() != 0 or not found:
                            raise ValueError("Loaded image is not the exact attested runtime config.")
                    except BaseException:
                        process.terminate()
                        raise
        finally:
            docker_tar.unlink(missing_ok=True)
            (directory / "uncompressed-layer.tar").unlink(missing_ok=True)


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, url):
        # Never forward registry credentials to another host.
        return None


class Registry:
    def __init__(self, image, push=True):
        if not re.fullmatch(r"ghcr\.io/[a-z0-9_.-]+/[a-z0-9_.-]+", image):
            raise ValueError("Expected ghcr.io/owner/repository.")
        self.name = image.removeprefix("ghcr.io/")
        self.base = f"https://ghcr.io/v2/{self.name}"
        self.opener = build_opener(NoRedirect)
        credentials = base64.b64encode(
            f"{os.environ['GITHUB_ACTOR']}:{os.environ['GH_TOKEN']}".encode()
        ).decode()
        url = "https://ghcr.io/token?" + urlencode({
            "service": "ghcr.io", "scope": f"repository:{self.name}:" + ("pull,push" if push else "pull"),
        })
        with self.opener.open(Request(url, headers={"Authorization": "Basic " + credentials}), timeout=60) as response:
            self.token = json.load(response)["token"]

    def request(self, method, path, data=None, headers=None, missing=False):
        if path.startswith("https://"):
            url = path
        elif path.startswith("/v2/"):
            url = "https://ghcr.io" + path
        else:
            url = self.base + path
        parsed = urlsplit(url)
        if parsed.scheme != "https" or parsed.netloc != "ghcr.io" or not parsed.path.startswith(f"/v2/{self.name}/"):
            raise ValueError("Registry returned an unsafe upload location.")
        request = Request(url, method=method, data=data, headers={
            "Authorization": "Bearer " + self.token, "Accept": ACCEPT, **(headers or {}),
        })
        try:
            return self.opener.open(request, timeout=600)
        except HTTPError as error:
            if missing and error.code == 404:
                return None
            raise

    def existing(self, tag, runtime, expected_index=None):
        response = self.request("GET", "/manifests/" + tag, missing=True)
        if response is None:
            return None
        with response:
            content = response.read()
            value = digest(content)
            if response.headers.get("Docker-Content-Digest") != value:
                raise ValueError("Registry manifest digest mismatch.")
        index = json.loads(content)
        if runtime_descriptor(index)["digest"] != runtime["digest"]:
            raise ValueError("Published image tag differs from the tested runtime. Never overwrite it; use a new version.")
        if expected_index is not None and value != expected_index:
            raise ValueError("Published image index or attestations differ. Never replace an existing tag.")
        if len(index["manifests"]) < 2:
            raise ValueError("Existing image has no attestations. Do not replace a published tag.")
        return value

    def fetch(self, tag, expected_runtime, expected_index, destination):
        response = self.request("GET", "/manifests/" + tag, missing=True)
        if response is None:
            return False
        with response:
            content = response.read()
            if response.headers.get("Docker-Content-Digest") != digest(content):
                raise ValueError("Registry manifest digest mismatch.")
            if digest(content) != expected_index:
                raise ValueError("Published OCI index differs from the immutable source manifest.")
        index = json.loads(content)
        if runtime_descriptor(index)["digest"] != expected_runtime:
            raise ValueError("Existing image does not match the immutable source manifest. Never replace its tag.")
        destination.parent.mkdir(parents=True, exist_ok=True)
        root = {"mediaType": INDEX, "digest": digest(content), "size": len(content)}
        seen = set()
        with tarfile.open(destination, "w") as archive:
            def add_bytes(name, data):
                info = tarfile.TarInfo(name)
                info.size = len(data)
                archive.addfile(info, io.BytesIO(data))

            def add(descriptor, data=None):
                value = descriptor["digest"]
                if not re.fullmatch(r"sha256:[0-9a-f]{64}", value) or descriptor["size"] < 0:
                    raise ValueError("Unsafe OCI descriptor from registry.")
                if value in seen:
                    return
                seen.add(value)
                if data is None:
                    route = "/manifests/" if descriptor["mediaType"] in (INDEX, MANIFEST) else "/blobs/"
                    try:
                        stream = self.request("GET", route + value)
                    except HTTPError as error:
                        location = error.headers.get("Location", "")
                        if error.code not in (302, 307) or not location.startswith("https://"):
                            raise
                        # GHCR blob storage redirects do not receive the registry token.
                        stream = self.opener.open(Request(location), timeout=600)
                    download = destination.with_suffix(".blob")
                    try:
                        with stream, download.open("wb") as output:
                            shutil.copyfileobj(stream, output)
                        with download.open("rb") as source:
                            if ("sha256:" + hashlib.file_digest(source, "sha256").hexdigest() != value
                                    or download.stat().st_size != descriptor["size"]):
                                raise ValueError(f"Registry blob checksum/size mismatch: {value}")
                            source.seek(0)
                            info = tarfile.TarInfo("blobs/sha256/" + value[7:])
                            info.size = descriptor["size"]
                            archive.addfile(info, source)
                        if route == "/manifests/":
                            data = download.read_bytes()
                    finally:
                        download.unlink(missing_ok=True)
                else:
                    add_bytes("blobs/sha256/" + value[7:], data)
                if descriptor["mediaType"] == INDEX:
                    for child in json.loads(data)["manifests"]:
                        add(child)
                elif descriptor["mediaType"] == MANIFEST:
                    document = json.loads(data)
                    for child in [document["config"], *document["layers"]]:
                        add(child)

            add(root, content)
            add_bytes("oci-layout", b'{"imageLayoutVersion":"1.0.0"}')
            add_bytes("index.json", json.dumps({"schemaVersion": 2, "manifests": [root]}).encode())
        return True

    def push(self, candidate, tag):
        existing = self.existing(tag, candidate.runtime, candidate.root["digest"])
        if existing:
            print(f"Reusing unchanged published runtime at {existing}; no tag or manifest replaced.")
            return existing

        def upload_blob(descriptor):
            response = self.request("HEAD", "/blobs/" + descriptor["digest"], missing=True)
            if response:
                response.close()
                return
            with self.request("POST", "/blobs/uploads/", data=b"") as response:
                location = response.headers["Location"]
            location += ("&" if "?" in location else "?") + urlencode({"digest": descriptor["digest"]})
            with candidate.stream(descriptor) as stream:
                with self.request("PUT", location, data=stream, headers={
                    "Content-Type": "application/octet-stream", "Content-Length": str(descriptor["size"]),
                }):
                    pass

        for descriptor in candidate.index["manifests"]:
            manifest = candidate.document(descriptor)
            for blob in [manifest["config"], *manifest["layers"]]:
                upload_blob(blob)
            with candidate.stream(descriptor) as stream:
                with self.request("PUT", "/manifests/" + descriptor["digest"], data=stream.read(),
                                  headers={"Content-Type": descriptor["mediaType"]}):
                    pass
        # All official writers share workflow concurrency. Check again immediately before
        # assigning the version tag, so an independently published version is not replaced.
        existing = self.existing(tag, candidate.runtime, candidate.root["digest"])
        if existing:
            return existing
        with candidate.stream(candidate.root) as stream:
            with self.request("PUT", "/manifests/" + tag, data=stream.read(),
                              headers={"Content-Type": INDEX}):
                pass
        result = self.existing(tag, candidate.runtime, candidate.root["digest"])
        if result != candidate.root["digest"]:
            raise ValueError("Published OCI index differs from the prepared artifact.")
        return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("fetch", "load", "push", "record-validation", "verify-validation", "verify-index"))
    parser.add_argument("--archive", type=Path, required=True)
    parser.add_argument("--image", required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--directory", type=Path, default=Path(".release/image-load"))
    parser.add_argument("--expected-runtime", default="")
    parser.add_argument("--expected-index", default="")
    parser.add_argument("--source-manifest", type=Path)
    parser.add_argument("--validation-receipt", type=Path, default=Path(".release/candidate-validation.json"))
    parser.add_argument("--revision", default="")
    args = parser.parse_args()
    release_version(args.tag)
    if args.command == "fetch":
        try:
            found = Registry(args.image, push=False).fetch(
                args.tag, args.expected_runtime, args.expected_index, args.archive
            )
        except HTTPError as error:
            if error.code not in (401, 403):
                raise
            print("Registry read unavailable (package may not exist yet). Preparing a candidate; "
                  "publication still requires the immutable-tag comparison.")
            found = False
        with open(os.environ["GITHUB_OUTPUT"], "a") as output:
            output.write(f"exists={'true' if found else 'false'}\n")
        return
    candidate = OCI(args.archive)
    try:
        if args.command == "verify-index":
            if (candidate.runtime["digest"] != args.expected_runtime
                    or candidate.root["digest"] != args.expected_index):
                raise ValueError("Recovered candidate differs from the immutable source manifest.")
            print("Recovered the exact OCI index and runtime recorded by the release.")
        elif args.command in ("record-validation", "verify-validation"):
            run_id = os.environ.get("GITHUB_RUN_ID", "")
            if args.command == "record-validation":
                receipt = validation_receipt(candidate, args.archive, args.revision, args.tag, run_id)
                args.validation_receipt.write_text(json.dumps(receipt, indent=2) + "\n")
            else:
                verify_validation(candidate, args.archive, json.loads(args.validation_receipt.read_text()),
                                  args.revision, args.tag, run_id)
            print("Exact OCI archive, release commit, version and validation checks verified.")
        elif args.command == "load":
            candidate.load(f"{args.image}:{args.tag}", args.directory)
        else:
            if not args.source_manifest:
                raise ValueError("Publication requires the verified corresponding-source manifest.")
            source = json.loads(args.source_manifest.read_text())
            labels = candidate.config["config"]["Labels"]
            expected_url = f"https://github.com/{args.image.removeprefix('ghcr.io/')}/releases/tag/{args.tag}/"
            if (source["release"] != args.tag or source["image_index_sha256"] != candidate.root["digest"]
                    or source["runtime_manifest_sha256"] != candidate.runtime["digest"]
                    or source["source_commit"] != labels.get("org.opencontainers.image.revision")
                    or labels.get("org.opencontainers.image.version") != release_version(args.tag)
                    or labels.get("io.github.filetwist.corresponding-source", "").lower() != expected_url.lower()):
                raise ValueError("OCI candidate does not match the verified versioned source manifest.")
            value = Registry(args.image).push(candidate, args.tag)
            print(f"Stored immutable OCI image: {args.image}@{value}")
            with open(os.environ["GITHUB_OUTPUT"], "a") as output:
                output.write(f"digest={value}\n")
    finally:
        candidate.archive.close()


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, tarfile.TarError, HTTPError, subprocess.CalledProcessError) as error:
        raise SystemExit(f"ERROR: {error}") from error
