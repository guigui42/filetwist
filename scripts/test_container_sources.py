"""Regression cases invoked by the existing Go test runner, without network access."""

import copy
import hashlib
import importlib.util
import io
import json
import shutil
import tarfile
import unittest
import uuid
from types import SimpleNamespace
from pathlib import Path
from unittest.mock import patch

spec = importlib.util.spec_from_file_location(
    "sources", Path(__file__).with_name("container-sources.py")
)
sources = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sources)
spec = importlib.util.spec_from_file_location(
    "image", Path(__file__).with_name("container-image.py")
)
image = importlib.util.module_from_spec(spec)
spec.loader.exec_module(image)


def fixture_oci(path, labels=None):
    blobs = {}

    def store(document, media_type):
        content = json.dumps(document, sort_keys=True).encode()
        value = image.digest(content)
        blobs["blobs/sha256/" + value[7:]] = content
        return {"mediaType": media_type, "digest": value, "size": len(content)}

    config = store({"config": {"Labels": labels or {}}, "architecture": "amd64", "os": "linux",
                    "rootfs": {"type": "layers", "diff_ids": []}}, "application/vnd.oci.image.config.v1+json")
    runtime = store({"schemaVersion": 2, "config": config, "layers": []}, image.MANIFEST)
    runtime["platform"] = {"architecture": "amd64", "os": "linux"}
    layers = [store({"predicateType": predicate}, "application/vnd.in-toto+json")
              for predicate in ("https://spdx.dev/Document", "https://slsa.dev/provenance/v1")]
    attestation = store({"schemaVersion": 2, "config": config, "layers": layers}, image.MANIFEST)
    attestation["annotations"] = {"vnd.docker.reference.digest": runtime["digest"]}
    attestation["platform"] = {"architecture": "unknown", "os": "unknown"}
    root = store({"schemaVersion": 2, "manifests": [runtime, attestation]}, image.INDEX)
    blobs["index.json"] = json.dumps({"schemaVersion": 2, "manifests": [root]}).encode()
    blobs["oci-layout"] = b'{"imageLayoutVersion":"1.0.0"}'
    with tarfile.open(path, "w") as archive:
        for name, content in blobs.items():
            info = tarfile.TarInfo(name)
            info.size = len(content)
            archive.addfile(info, io.BytesIO(content))
    return runtime, blobs


class SourceTests(unittest.TestCase):
    def test_shared_release_metadata(self):
        revision = "a" * 40
        metadata = sources.release_metadata("Owner/Project", "v12.4.0-rc.2", revision)
        self.assertEqual(metadata["version"], "12.4.0-rc.2")
        self.assertEqual(metadata["image"], "ghcr.io/owner/project")
        self.assertEqual(metadata["source_url"], "https://github.com/Owner/Project/releases/tag/v12.4.0-rc.2/")
        ci = sources.release_metadata("owner/project", "", revision)
        self.assertEqual(ci["version"], revision)
        self.assertEqual(ci["source_url"], "")
        with self.assertRaises(ValueError):
            sources.release_metadata("owner/project", "v1.0.0", "main")

    def test_tags(self):
        for tag in ("v0.1.0", "v12.3.4-rc.1"):
            self.assertEqual(sources.version(tag), tag[1:])
        for tag in ("main", "v1.2.3+build", "../v1.2.3", "v1.2.3\n", "v1.2.3-" + "a" * 128):
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                sources.version(tag)

    def test_inventory_and_epoch(self):
        inventory = sources.parse_inventory(
            b"Binary package\tBinary version\tSource package\tSource version\n"
            b"libdemo:amd64\t2:1.0-3+b1\tdemo\t2:1.0-3\n"
        )
        self.assertEqual(inventory[0]["version"], "2:1.0-3")
        self.assertEqual(inventory[0]["binary_packages"][0]["name"], "libdemo:amd64")
        uris = (
            "'http://snapshot.debian.org/archive/debian/20260905T000000Z/"
            "pool/main/d/demo/demo_1.0-3.dsc' demo_1.0-3.dsc 4 SHA256:" + "a" * 64 + "\n"
            "'http://snapshot.debian.org/archive/debian/20260905T000000Z/"
            "pool/main/d/demo/demo_1.0.orig.tar.xz' demo_1.0.orig.tar.xz 5 SHA256:" + "b" * 64
        )
        sources.attach_uris(inventory, uris, "20260905T000000Z")
        self.assertEqual(len(inventory[0]["source_files"]), 2)
        self.assertTrue(inventory[0]["source_files"][0]["url"].startswith("https://"))
        sources.attach_uris(inventory, uris.replace("/archive/debian/", "/archive/debian-security/")
                            .replace("/pool/main/", "/pool/updates/main/"), "20260905T000000Z")
        self.assertEqual(len(inventory[0]["source_files"]), 2)
        for old, new in (
            ("demo_1.0-3.dsc", "demo_2.0-3.dsc"),
            ("snapshot.debian.org", "example.com"),
            ("20260905T000000Z", "20200101T000000Z"),
            ("SHA256:", "MD5Sum:"),
            (" demo_1.0-3.dsc ", " ../demo_1.0-3.dsc "),
        ):
            with self.subTest(new=new), self.assertRaises(ValueError):
                sources.attach_uris(copy.deepcopy(inventory), uris.replace(old, new), "20260905T000000Z")

    def test_reject_unsafe_archive_members(self):
        for name, kind in (("../escape", tarfile.REGTYPE), ("/absolute", tarfile.REGTYPE),
                           ("..\\escape", tarfile.REGTYPE), ("file\nname", tarfile.REGTYPE),
                           ("safe/link", tarfile.SYMTYPE), ("safe/hard", tarfile.LNKTYPE)):
            with self.subTest(name=name):
                blob = io.BytesIO()
                with tarfile.open(fileobj=blob, mode="w") as archive:
                    entry = tarfile.TarInfo(name)
                    entry.type = kind
                    entry.linkname = "../../escape"
                    archive.addfile(entry)
                blob.seek(0)
                with tarfile.open(fileobj=blob) as archive, self.assertRaises(ValueError):
                    sources.read_archive(archive)

    def test_deterministic_archive(self):
        entries = {"b/source.dsc": b"descriptor\n", "a/LICENSE": b"notice\n"}
        first = sources.archive_bytes(entries)
        self.assertEqual(first, sources.archive_bytes(dict(reversed(list(entries.items())))))
        with tarfile.open(fileobj=io.BytesIO(first)) as archive:
            self.assertEqual(sources.read_archive(archive), entries)

    def test_idempotent_assets(self):
        data = b"exact reviewed source\n"
        assets = {"source.tar.gz": data}
        digest = hashlib.sha256(data).hexdigest()
        existing = [{"name": "source.tar.gz", "digest": "sha256:" + digest, "id": 7}]
        with patch.object(sources, "release_assets", return_value=existing), \
                patch.object(sources, "asset_bytes", return_value=data), \
                patch.object(sources, "upload_asset") as upload:
            sources.publish_assets("owner/repo", "v0.1.0", assets)
            upload.assert_not_called()
        with patch.object(sources, "release_assets", return_value=existing), \
                patch.object(sources, "asset_bytes", return_value=b"changed"), \
                patch.object(sources, "upload_asset") as upload:
            with self.assertRaises(ValueError):
                sources.publish_assets("owner/repo", "v0.1.0", assets)
            upload.assert_not_called()

    def test_policy_is_not_automatically_approved(self):
        actual = [{"name": "demo", "version": "1.0", "binary_packages": [
            {"name": "demo", "version": "1.0", "notice_sha256": "a" * 64}
        ]}]
        policy = {"schema_version": 1, "debian_sources": copy.deepcopy(actual)}
        policy["debian_sources"][0].update(source_route="mirror", reason="Reviewed licence")
        sources.apply_policy(actual, policy)
        self.assertEqual(actual[0]["source_route"], "mirror")
        for change in ("version", "notice", "unknown", "route"):
            candidate, review = copy.deepcopy(actual), copy.deepcopy(policy)
            if change == "version":
                candidate[0]["version"] = "2.0"
            elif change == "notice":
                candidate[0]["binary_packages"][0]["notice_sha256"] = "b" * 64
            elif change == "unknown":
                candidate.append({"name": "new", "version": "1.0", "binary_packages": []})
            else:
                review["debian_sources"][0]["source_route"] = "pending"
            with self.subTest(change=change), self.assertRaises(ValueError):
                sources.apply_policy(candidate, review)

    def test_built_using_closure_preserves_epochs_versions_and_notices(self):
        packages = [{"name": "consumer-src", "version": "3", "binary_packages": [
            {"name": "consumer:amd64", "version": "3+b1", "notice_sha256": "a" * 64}
        ]}]
        status = (
            "Package: consumer\nArchitecture: amd64\nVersion: 3+b1\n"
            "Source: consumer-src (3)\nStatus: install ok installed\n"
            "Built-Using: unicode-data (= 15.1.0-1), rust-demo (= 1:1.0-1)\n"
            "Static-Built-Using: rust-demo (= 1:1.0-1),\n rust-demo (= 2:2.0-1)\n"
        ).encode()
        closure = sources.add_built_using(copy.deepcopy(packages), status)
        self.assertEqual([sources.source_key(item) for item in closure], [
            ("consumer-src", "3"), ("rust-demo", "1:1.0-1"),
            ("rust-demo", "2:2.0-1"), ("unicode-data", "15.1.0-1"),
        ])
        first = closure[1]
        self.assertEqual(first["binary_packages"], [])
        self.assertEqual([item["field"] for item in first["built_using"]],
                         ["Built-Using", "Static-Built-Using"])
        self.assertEqual(first["built_using"][0]["notice_sha256"], "a" * 64)
        policy = {"schema_version": 1, "debian_sources": copy.deepcopy(packages)}
        with self.assertRaises(ValueError):
            sources.apply_policy(closure, policy)
        for old, new in (
            ("unicode-data (= 15.1.0-1)", "unicode-data (>= 15.1.0-1)"),
            ("unicode-data (= 15.1.0-1)", "unicode-data"),
            ("Version: 3+b1", "Version: 3+b2"),
            ("Source: consumer-src (3)", "Source: different-source (3)"),
        ):
            with self.subTest(change=new), self.assertRaises(ValueError):
                sources.add_built_using(copy.deepcopy(packages), status.replace(old.encode(), new.encode()))
        with self.assertRaises(ValueError):
            sources.add_built_using(copy.deepcopy(packages), b"")

    def test_version_scoped_source_queries_and_cache_paths(self):
        packages = [{"name": "demo", "version": value, "binary_packages": []}
                    for value in ("1:1.0-1", "1:1.0-2")]
        blocks = []
        for package in packages:
            descriptor = f"demo_{package['version'].split(':', 1)[1]}.dsc"
            blocks.append(f"FILETWIST-SOURCE\tdemo\t{package['version']}\n")
            for name in (descriptor, "demo_1.0.orig.tar.xz"):
                blocks.append(
                    f"'https://snapshot.debian.org/archive/debian/20260905T000000Z/pool/main/d/demo/{name}' "
                    f"{name} 5 SHA256:" + "a" * 64 + "\n"
                )
        sources.attach_uris(packages, "".join(blocks), "20260905T000000Z")
        self.assertEqual([len(package["source_files"]) for package in packages], [2, 2])
        self.assertNotEqual(sources.source_path(packages[0], {"name": "source.tar.xz"}),
                            sources.source_path(packages[1], {"name": "source.tar.xz"}))
        self.assertIn("1%3A1.0-1", sources.source_path(packages[0], {"name": "source.tar.xz"}))
        unscoped = "".join(line for line in blocks if not line.startswith("FILETWIST-SOURCE"))
        with self.assertRaises(ValueError):
            sources.attach_uris(packages, unscoped, "20260905T000000Z")

    def test_source_routes_do_not_clear_publication(self):
        for policy in (
            {}, {"status": "approved"},
            {"status": "pending_maintainer_legal_disposition", "blockers": []},
            {"status": "approved", "blockers": [
                {"id": "ffmpeg-zvbi-combination", "status": "unresolved", "source_route": "mirror"}
            ]},
            {"status": "approved", "blockers": [{"id": "notices", "status": "resolved"}]},
        ):
            with self.subTest(policy=policy), self.assertRaises(ValueError):
                sources.require_publication_clearance(policy)
        sources.require_publication_clearance({"status": "approved", "blockers": []})
        sources.require_publication_clearance({"status": "approved", "blockers": [
            {"id": "fixture-blocker", "status": "resolved", "resolution": "Synthetic reviewed disposition"}
        ]})

    def test_descriptor_source_completeness(self):
        directory = Path(".release/source-tests") / str(uuid.uuid4())
        directory.mkdir(parents=True)
        self.addCleanup(shutil.rmtree, directory)
        path = directory / "demo.dsc"
        path.write_text("Source: demo\nVersion: 2:1.0-3\nChecksums-Sha256:\n "
                        + "a" * 64 + " 5 demo_1.0.orig.tar.xz\n")
        package = {"name": "demo", "version": "2:1.0-3", "source_files": [
            {"name": "demo_1.0-3.dsc", "sha256": "b" * 64, "size": 200},
            {"name": "demo_1.0.orig.tar.xz", "sha256": "a" * 64, "size": 5},
        ]}
        sources.verify_descriptor(path, package)
        package["source_files"].append({"name": "omitted.debian.tar.xz", "sha256": "c" * 64, "size": 3})
        with self.assertRaises(ValueError):
            sources.verify_descriptor(path, package)

    def test_embedded_and_supplemental_source_notices(self):
        directory = Path(".release/source-tests") / str(uuid.uuid4())
        directory.mkdir(parents=True)
        self.addCleanup(shutil.rmtree, directory)
        notice = b"Synthetic redistributable licence notice\n"
        data = sources.archive_bytes({"demo/LICENSE": notice, "debian/copyright": notice})
        package = {"name": "demo", "version": "1", "source_route": "mirror",
                   "built_using": [{"binary_package": "consumer"}], "source_files": [
                       {"name": "demo_1.tar.gz", "sha256": sources.sha256(data),
                        "size": len(data), "url": "https://example.test/demo_1.tar.gz"}
                   ]}
        path = directory / sources.source_path(package, package["source_files"][0])
        path.parent.mkdir(parents=True)
        path.write_bytes(data)
        extra = {"source": "demo", "version": "1", "archive": "demo_1.tar.gz",
                 "member": "demo/LICENSE", "sha256": sources.sha256(notice)}
        policy = {"supplemental_notices": [extra]}
        retained = sources.source_notices([package], policy, directory)
        self.assertEqual(len(retained), 3)
        self.assertEqual(retained["source/supplemental-notices/demo/demo/LICENSE"], notice)
        extra["sha256"] = "0" * 64
        with self.assertRaisesRegex(ValueError, "Supplemental notice changed"):
            sources.source_notices([package], policy, directory)
        extra["member"] = "missing"
        with self.assertRaisesRegex(ValueError, "Missing or invalid"):
            sources.source_notices([package], policy, directory)
        path.write_bytes(sources.archive_bytes({"demo/program.c": b"synthetic source"}))
        with self.assertRaisesRegex(ValueError, "Missing accompanying"):
            sources.source_notices([package], {}, directory)

    def test_review_template_never_approves_publication(self):
        directory = Path(".release/source-tests") / str(uuid.uuid4())
        directory.mkdir(parents=True)
        self.addCleanup(shutil.rmtree, directory)
        files = {
            sources.LICENSES + "Dockerfile": b"synthetic recipe",
            sources.VIPS + "SOURCE": (
                "https://github.com/libvips/libvips/releases/download/v3.2.1/vips-3.2.1.tar.xz\n"
                "SHA256: " + "a" * 64 + "\nBuilt without source modifications.\n"
            ).encode(),
            sources.VIPS + "LICENSE": b"synthetic LGPL notice",
        }
        inventory = directory / "inventory.tar.gz"
        inventory.write_bytes(sources.archive_bytes(files))
        args = SimpleNamespace(inventory=inventory, checkout=Path("."), revision="a" * 40,
                               first_party_asset=[], additional_path=[])
        with patch.object(sources, "candidate_sources", return_value=([], None, "20260905T000000Z")), \
                patch.object(sources, "run", return_value=sources.archive_bytes({"go.mod": b"module demo\n"})), \
                patch("builtins.print") as printed:
            sources.review_template(args)
        policy = json.loads(printed.call_args.args[0])
        self.assertEqual(policy["status"], "pending")
        with self.assertRaises(ValueError):
            sources.require_publication_clearance(policy)

    def test_custom_ffmpeg_requires_its_own_review_and_complete_source(self):
        descriptor, original = b"exact ffmpeg descriptor", b"exact ffmpeg source"
        version = "7:7.1.5-0+deb13u1"
        name = "ffmpeg_7.1.5-0+deb13u1.dsc"
        files = {
            sources.FFMPEG + "SOURCE": (
                f"Source: ffmpeg\nVersion: {version}\nSnapshot: 20260905T000000Z\n"
                f"Descriptor: {name}\nSHA256: {sources.sha256(descriptor)}\n"
            ).encode(),
            sources.FFMPEG + name: descriptor,
            sources.FFMPEG + "copyright": b"reviewed notice",
            sources.FFMPEG + "configure-flags.txt": b"--enable-gpl\n--disable-libzvbi\n",
            sources.FFMPEG + "build-ffmpeg.sh": b"exact build recipe",
            sources.FFMPEG + "SHA256SUMS": (
                f"{sources.sha256(descriptor)}  {name}\n"
                f"{sources.sha256(original)}  ffmpeg_7.1.5.orig.tar.xz\n"
            ).encode(),
        }
        custom = sources.custom_ffmpeg(files, "20260905T000000Z")
        custom["source_files"] = [
            {"name": name, "sha256": sources.sha256(descriptor)},
            {"name": "ffmpeg_7.1.5.orig.tar.xz", "sha256": sources.sha256(original)},
        ]
        self.assertEqual(custom["binary_packages"], [])
        with self.assertRaises(ValueError):
            sources.apply_custom_policy(custom, {}, files)
        policy = {"custom_builds": {"ffmpeg": {
            "version": version, "build_material_sha256": custom["build_material_sha256"],
            "source_route": "mirror", "reason": "Synthetic reviewed source-built stack",
        }}}
        sources.apply_custom_policy(custom, policy, files)
        self.assertEqual(custom["source_route"], "mirror")
        custom["source_files"].pop()
        with self.assertRaises(ValueError):
            sources.apply_custom_policy(custom, policy, files)
        with self.assertRaises(ValueError):
            sources.custom_ffmpeg(files, "20200101T000000Z")
        files[sources.FFMPEG + name] = b"changed descriptor"
        with self.assertRaises(ValueError):
            sources.custom_ffmpeg(files, "20260905T000000Z")

    def test_restore_mirrors_without_upstream(self):
        directory = Path(".release/source-tests") / str(uuid.uuid4())
        directory.mkdir(parents=True)
        self.addCleanup(shutil.rmtree, directory)
        data = b"exact source"
        expected = {"debian/demo/source.tar.xz": {"sha256": sources.sha256(data)}}
        archive = sources.archive_bytes({"debian/demo/source.tar.xz": data})
        with patch.object(sources, "release_assets", return_value=[{"name": "mirrors.tar.gz"}]), \
                patch.object(sources, "asset_bytes", return_value=archive):
            sources.restore_mirrors("owner/repo", "v0.1.0", "mirrors.tar.gz", expected, directory)
            self.assertEqual((directory / "debian/demo/source.tar.xz").read_bytes(), data)
            expected["debian/demo/source.tar.xz"]["sha256"] = "a" * 64
            with self.assertRaises(ValueError):
                sources.restore_mirrors("owner/repo", "v0.1.0", "mirrors.tar.gz", expected, directory)

    def test_source_cache_rejects_parent_symlinks(self):
        directory = Path(".release/source-tests") / str(uuid.uuid4())
        directory.mkdir(parents=True)
        self.addCleanup(shutil.rmtree, directory)
        (directory / "target").mkdir()
        (directory / "link").symlink_to("target", target_is_directory=True)
        with self.assertRaisesRegex(ValueError, "symlink"), patch.object(sources, "run") as command:
            sources.fetch({"url": "https://example.test/source", "sha256": "a" * 64},
                          directory / "link/source.tar.xz")
        command.assert_not_called()

    def test_prepare_complete_repeatable_release(self):
        directory = Path(".release/source-tests") / str(uuid.uuid4())
        directory.mkdir(parents=True)
        self.addCleanup(shutil.rmtree, directory)
        revision, tag, repo = "a" * 40, "v12.4.0-rc.2", "owner/project"
        archive_source, vips_source = b"debian upstream source", b"libvips upstream source"
        descriptor = ("Source: demo\nVersion: 2:1.0-3\nChecksums-Sha256:\n "
                      f"{sources.sha256(archive_source)} {len(archive_source)} demo_1.0.orig.tar.xz\n").encode()
        inventory = (b"Binary package\tBinary version\tSource package\tSource version\n"
                     b"libdemo:amd64\t2:1.0-3+b1\tdemo\t2:1.0-3\n")
        vips_url = "https://github.com/libvips/libvips/releases/download/v3.2.1/vips-3.2.1.tar.xz"
        recipe = b"FROM scratch\n"
        application_archive = sources.archive_bytes({"go.mod": b"module example.test/demo\n"})
        files = {
            sources.LICENSES + "debian-packages.tsv": inventory,
            sources.LICENSES + "debian-snapshot.txt": b"20260905T000000Z\n",
            sources.LICENSES + "Dockerfile": recipe,
            sources.LICENSES + "go.LICENSE": b"Go notice",
            sources.LICENSES + "htmx.LICENSE": b"HTMX notice",
            sources.VIPS + "LICENSE": b"libvips notice",
            sources.VIPS + "SOURCE": (vips_url + "\nSHA256: " + sources.sha256(vips_source)
                                     + "\nBuilt without source modifications.\n").encode(),
            "usr/share/doc/libdemo/copyright": b"Debian notice",
            "usr/share/common-licenses/GPL-2": b"GPL notice",
            sources.DPKG_STATUS: (
                b"Package: libdemo\nArchitecture: amd64\nVersion: 2:1.0-3+b1\n"
                b"Source: demo (2:1.0-3)\nStatus: install ok installed\n"
            ),
        }
        policy = {
            "schema_version": 1, "dockerfile_sha256": sources.sha256(recipe),
            "first_party_assets": [], "additional_dependency_paths": [],
            "dependency_inputs_sha256": sources.dependency_inputs(application_archive, [], []),
            "status": "pending_maintainer_legal_disposition",
            "blockers": [{"id": "synthetic-combination-review", "status": "unresolved"}],
            "debian_sources": [{"name": "demo", "version": "2:1.0-3", "binary_packages": [
                {"name": "libdemo:amd64", "version": "2:1.0-3+b1",
                 "notice_sha256": sources.sha256(b"Debian notice")}
            ], "source_route": "mirror", "reason": "Synthetic reviewed route"}],
            "notice_sha256": {name: sources.sha256(files[name]) for name in (
                sources.LICENSES + "go.LICENSE", sources.LICENSES + "htmx.LICENSE",
                sources.VIPS + "LICENSE", "usr/share/common-licenses/GPL-2")},
            "libvips": {"version": "3.2.1", "url": vips_url, "sha256": sources.sha256(vips_source),
                        "notice_sha256": sources.sha256(b"libvips notice"),
                        "source_route": "upstream", "reason": "Synthetic reviewed route"},
        }
        (directory / "inventory.tar.gz").write_bytes(sources.archive_bytes(files))
        (directory / "policy.json").write_text(json.dumps(policy))
        source_files = {"demo_1.0-3.dsc": descriptor, "demo_1.0.orig.tar.xz": archive_source}
        (directory / "uris.txt").write_text("".join(
            f"'http://snapshot.debian.org/archive/debian/20260905T000000Z/pool/main/d/demo/{name}' "
            f"{name} {len(content)} SHA256:{sources.sha256(content)}\n"
            for name, content in source_files.items()
        ))
        fixture_oci(directory / "candidate.tar", {
            "org.opencontainers.image.revision": revision,
            "org.opencontainers.image.version": tag[1:],
            "io.github.filetwist.corresponding-source": f"https://github.com/{repo}/releases/tag/{tag}/",
        })
        args = SimpleNamespace(tag=tag, repository=repo, revision=revision, checkout=directory,
                               inventory=directory / "inventory.tar.gz", uris=directory / "uris.txt",
                               policy=directory / "policy.json", candidate=directory / "candidate.tar",
                               output=directory / "first")

        def command(*values, **kwargs):
            if values[0] == "git":
                if values[-1] == "HEAD":
                    return revision.encode()
                if values[-1] == "HEAD:deploy/Dockerfile":
                    return recipe
                return application_archive
            if values[0] == "curl":
                Path(values[values.index("--output") + 1]).write_bytes(source_files[values[-1].split("/")[-1]])
                return b""
            self.fail(f"Unexpected subprocess: {values}")

        with patch.object(sources, "run", side_effect=command), \
                patch.object(sources, "release_assets", return_value=[]), \
                patch.object(sources, "check_link") as links:
            sources.prepare(args)
            links.assert_called_once_with(vips_url)
            args.output = directory / "second"
            sources.prepare(args)
        first = {p.name: sources.sha256(p) for p in (directory / "first").glob("filetwist_*")}
        second = {p.name: sources.sha256(p) for p in (directory / "second").glob("filetwist_*")}
        self.assertEqual(first, second)
        self.assertEqual(len(first), 5)
        manifest = json.loads((args.output / "filetwist_12.4.0-rc.2_sources.json").read_text())
        self.assertEqual(manifest["release"], tag)
        self.assertEqual(manifest["source_commit"], revision)
        self.assertEqual(manifest["debian_sources"][0]["version"], "2:1.0-3")
        self.assertTrue(manifest["runtime_manifest_sha256"].startswith("sha256:"))
        for command_name in ("publish", "verify"):
            with patch.object(sources, "resolve"), patch.object(sources, "publish_assets") as upload, \
                    patch.object(sources, "verify_assets") as verify, self.assertRaises(ValueError):
                sources.publish(SimpleNamespace(command=command_name, tag=tag, repository=repo,
                                                directory=args.output))
            upload.assert_not_called()
            verify.assert_not_called()
        retained = {p.name: p.read_bytes() for p in (directory / "first").glob("filetwist_*")}
        args.policy.write_text('{"newer_dependency_review": "must not replace the old review"}')
        args.output = directory / "third"

        def no_upstream_download(*values, **kwargs):
            if values[0] == "curl":
                self.fail("Historical mirrored sources must not be downloaded from their original host.")
            return command(*values, **kwargs)

        with patch.object(sources, "run", side_effect=no_upstream_download), \
                patch.object(sources, "release_assets", return_value=[
                    {"name": name, "id": name} for name in retained]), \
                patch.object(sources, "asset_bytes", side_effect=lambda repo, asset: retained[asset["id"]]), \
                patch.object(sources, "check_link"):
            sources.prepare(args)
        third = {p.name: sources.sha256(p) for p in args.output.glob("filetwist_*")}
        self.assertEqual(first, third, "Retry must preserve historical review and archive bytes")


class ImageTests(unittest.TestCase):
    runtime = {"mediaType": image.MANIFEST, "digest": "sha256:" + "a" * 64,
               "size": 100, "platform": {"architecture": "amd64", "os": "linux"}}

    def test_runtime_platform(self):
        self.assertEqual(image.runtime_descriptor({"manifests": [self.runtime]}), self.runtime)
        for descriptors in ([], [self.runtime, self.runtime],
                            [{**self.runtime, "platform": {"architecture": "arm64", "os": "linux"}}]):
            with self.assertRaises(ValueError):
                image.runtime_descriptor({"manifests": descriptors})

    def test_existing_version_is_never_replaced(self):
        content = json.dumps({"manifests": [self.runtime, {"platform": {"os": "unknown"}}]}).encode()
        registry = image.Registry.__new__(image.Registry)
        response = io.BytesIO(content)
        response.headers = {"Docker-Content-Digest": image.digest(content)}
        with patch.object(registry, "request", return_value=response) as request:
            self.assertEqual(registry.existing("v0.1.0", self.runtime), image.digest(content))
            request.assert_called_once_with("GET", "/manifests/v0.1.0", missing=True)
        response = io.BytesIO(content)
        response.headers = {"Docker-Content-Digest": image.digest(content)}
        with patch.object(registry, "request", return_value=response), self.assertRaises(ValueError):
            registry.existing("v0.1.0", {**self.runtime, "digest": "sha256:" + "b" * 64})
        response = io.BytesIO(content)
        response.headers = {"Docker-Content-Digest": image.digest(content)}
        with patch.object(registry, "request", return_value=response), self.assertRaises(ValueError):
            registry.existing("v0.1.0", self.runtime, "sha256:" + "c" * 64)

    def test_retry_does_not_upload_anything(self):
        registry = image.Registry.__new__(image.Registry)
        candidate = type("Candidate", (), {"runtime": self.runtime, "root": {"digest": "sha256:" + "c" * 64}})()
        with patch.object(registry, "existing", return_value="sha256:" + "c" * 64), \
                patch.object(registry, "request") as request:
            self.assertEqual(registry.push(candidate, "v0.1.0"), "sha256:" + "c" * 64)
            request.assert_not_called()

    def test_credentials_cannot_follow_upload_redirects(self):
        registry = image.Registry.__new__(image.Registry)
        registry.base, registry.name = "https://ghcr.io/v2/owner/repo", "owner/repo"
        for location in ("https://evil.example/upload", "http://ghcr.io/v2/owner/repo/upload",
                         "https://ghcr.io/v2/other/repo/upload"):
            with self.subTest(location=location), self.assertRaises(ValueError):
                registry.request("PUT", location, b"source")

    def test_corrupt_oci_blobs_are_rejected(self):
        directory = Path(".release/source-tests") / str(uuid.uuid4())
        directory.mkdir(parents=True)
        self.addCleanup(shutil.rmtree, directory)
        archive = directory / "candidate.tar"
        descriptor = {"digest": "sha256:" + "a" * 64, "size": 5, "mediaType": image.INDEX}
        with tarfile.open(archive, "w") as target:
            for name, content in {
                "index.json": json.dumps({"manifests": [descriptor]}).encode(),
                "blobs/sha256/" + "a" * 64: b"wrong",
            }.items():
                info = tarfile.TarInfo(name)
                info.size = len(content)
                target.addfile(info, io.BytesIO(content))
        with self.assertRaisesRegex(ValueError, "checksum mismatch"):
            image.OCI(archive)

    def test_publish_preserves_complete_oci_graph(self):
        directory = Path(".release/source-tests") / str(uuid.uuid4())
        directory.mkdir(parents=True)
        self.addCleanup(shutil.rmtree, directory)
        archive = directory / "candidate.tar"
        fixture_oci(archive)
        candidate = image.OCI(archive)
        self.addCleanup(candidate.archive.close)
        registry = image.Registry.__new__(image.Registry)
        written = {}

        def request(method, path, data=None, headers=None, missing=False):
            if method == "HEAD":
                return None
            result = io.BytesIO(b"")
            result.headers = {"Location": "/v2/owner/repo/blobs/uploads/upload-id"}
            if method == "PUT":
                written[path] = data.read() if hasattr(data, "read") else data
            return result

        with patch.object(registry, "request", side_effect=request), \
                patch.object(registry, "existing", side_effect=[None, None, candidate.root["digest"]]):
            self.assertEqual(registry.push(candidate, "v0.1.0"), candidate.root["digest"])
        self.assertEqual(image.digest(written["/manifests/v0.1.0"]), candidate.root["digest"])
        for descriptor in candidate.index["manifests"]:
            self.assertEqual(image.digest(written["/manifests/" + descriptor["digest"]]), descriptor["digest"])
        blobs = [data for name, data in written.items() if "/blobs/uploads/" in name]
        self.assertTrue(any(b"https://spdx.dev/Document" in data for data in blobs))
        self.assertTrue(any(b"https://slsa.dev/provenance/v1" in data for data in blobs))

    def test_ci_reuse_requires_exact_archive_commit_tag_run_and_checks(self):
        directory = Path(".release/source-tests") / str(uuid.uuid4())
        directory.mkdir(parents=True)
        self.addCleanup(shutil.rmtree, directory)
        archive = directory / "candidate.tar"
        revision, tag, run_id = "a" * 40, "v1.2.3", "1234"
        fixture_oci(archive, {"org.opencontainers.image.revision": revision,
                              "org.opencontainers.image.version": "1.2.3"})
        candidate = image.OCI(archive)
        self.addCleanup(candidate.archive.close)
        receipt = image.validation_receipt(candidate, archive, revision, tag, run_id)
        image.verify_validation(candidate, archive, receipt, revision, tag, run_id)
        for field, value in (
            ("source_commit", "b" * 40), ("tag", "v2.0.0"), ("run_id", "5678"),
            ("archive_sha256", "b" * 64), ("image_index_sha256", "sha256:" + "b" * 64),
            ("runtime_manifest_sha256", "sha256:" + "b" * 64),
            ("checks", ["conversions", "size-budget"]), ("schema_version", 2),
        ):
            changed = {**receipt, field: value}
            with self.subTest(field=field), self.assertRaises(ValueError):
                image.verify_validation(candidate, archive, changed, revision, tag, run_id)
        with self.assertRaises(ValueError):
            image.validation_receipt(candidate, archive, "b" * 40, tag, run_id)
        with self.assertRaises(ValueError):
            image.validation_receipt(candidate, archive, revision, tag, "")
        with archive.open("ab") as stream:
            stream.write(b"changed archive bytes")
        with self.assertRaises(ValueError):
            image.verify_validation(candidate, archive, receipt, revision, tag, run_id)


if __name__ == "__main__":
    unittest.main()
