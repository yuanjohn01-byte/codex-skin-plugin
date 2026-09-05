#!/usr/bin/env python3
"""Synthetic candidate tamper tests; no signing secret or installed Plugin used."""

from __future__ import annotations

import hashlib
import copy
import json
import tempfile
import unittest
import zipfile
from pathlib import Path
from unittest.mock import patch

import windows_test_candidate as candidate
import verify_windows_test_run as evidence
from create_release_descriptor import canonical_bytes, descriptor_from_summary
from test_ci_workflows import job_block
from test_release_profiles import bootstrap_summary, helper_summary


SHA = "a" * 40


def fixture(root: Path) -> None:
    for directory, summary in (
        ("helper", helper_summary(candidate.PROFILE)),
        ("bootstrap", bootstrap_summary(candidate.PROFILE)),
    ):
        (root / directory).mkdir(parents=True)
        for index, item in enumerate(summary["artifacts"]):
            data = f"synthetic {directory} {index}".encode()
            item["size"] = len(data)
            item["sha256"] = hashlib.sha256(data).hexdigest()
            (root / directory / item["filename"]).write_bytes(data)
        (root / directory / "build-summary.json").write_text(json.dumps(summary))
        if directory == "helper":
            descriptor = descriptor_from_summary(summary, candidate.PROFILE.signing_key_id, candidate.PROFILE.name)
            (root / directory / "helper-release-descriptor.json").write_bytes(canonical_bytes(descriptor))


class CandidateTests(unittest.TestCase):
    def test_profiles_leave_released_versions_unchanged(self):
        from release_profiles import PRODUCTION, STAGING
        self.assertEqual(PRODUCTION.helper_version, "0.1.0-paid-alpha.17")
        self.assertEqual(STAGING.helper_version, "0.1.0-paid-alpha.16")
        self.assertEqual(candidate.PROFILE.helper_version, "0.1.0-paid-alpha.17.windows.1")
        self.assertEqual(candidate.PROFILE.bootstrap_version, "0.1.0-paid-alpha.16.windows.1")
        self.assertEqual(candidate.PROFILE.signing_key_id, PRODUCTION.signing_key_id)

    def test_freeze_rejects_non_sha_dirty_tree_and_existing_tag(self):
        for value in ("main", SHA[:7], "../main", SHA + "\n"):
            with self.assertRaises(ValueError):
                candidate.freeze(value)
        for outputs in (("b" * 40,), (SHA, " M file"), (SHA, "", candidate.PROFILE.helper_release_tag)):
            with patch.object(candidate, "run", side_effect=outputs), self.assertRaises(ValueError):
                candidate.freeze(SHA)
        with patch.object(candidate, "run", side_effect=(SHA, "", "", "2026-09-05T00:00:00Z")):
            self.assertEqual(candidate.freeze(SHA), (SHA, "2026-09-05T00:00:00Z"))

    def test_payload_rejects_tampered_binary_descriptor_and_provenance(self):
        for mutation in ("binary", "descriptor", "commit", "timestamp", "symlink"):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                fixture(root)
                candidate.verify_payload(root, SHA)
                summary_path = root / "bootstrap/build-summary.json"
                summary = json.loads(summary_path.read_text())
                binary = root / "bootstrap" / summary["artifacts"][0]["filename"]
                if mutation == "binary":
                    binary.write_bytes(b"corrupt")
                elif mutation == "descriptor":
                    with (root / "helper/helper-release-descriptor.json").open("ab") as handle:
                        handle.write(b" ")
                elif mutation == "symlink":
                    data = binary.read_bytes()
                    binary.unlink()
                    (root / "external").write_bytes(data)
                    binary.symlink_to(root / "external")
                else:
                    summary["artifacts"][0]["buildCommit" if mutation == "commit" else "builtAt"] = "changed"
                    summary_path.write_text(json.dumps(summary))
                with self.assertRaises(ValueError):
                    candidate.verify_payload(root, SHA)

    def test_package_is_deterministic_and_does_not_edit_source(self):
        sha = candidate.run("git", "rev-parse", "HEAD")
        source = candidate.ROOT / "plugins/codex-skin/scripts/bootstrap-pins.ps1"
        before = source.read_bytes()
        archives = []
        for _ in range(2):
            with tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                fixture(root)
                version = candidate.package_plugin(root, sha)
                self.assertTrue(version.endswith(f"+codex.{sha}"))
                archive_path = root / "windows-test-marketplace.zip"
                archives.append(archive_path.read_bytes())
                with zipfile.ZipFile(archive_path) as archive:
                    names = archive.namelist()
                    self.assertIn(".agents/plugins/marketplace.json", names)
                    self.assertTrue(all(name.startswith(("plugins/codex-skin/", ".agents/plugins/")) or name in {"LICENSE", "NOTICE"} for name in names))
                    self.assertIn(candidate.PROFILE.helper_release_tag.encode(), archive.read("plugins/codex-skin/scripts/bootstrap-pins.ps1"))
                    skill = archive.read("plugins/codex-skin/skills/codex-skin-version/SKILL.md").decode()
                    for value in (sha, version, candidate.PROFILE.helper_version, candidate.PROFILE.bootstrap_version, "not proof that the Helper"):
                        self.assertIn(value, skill)
                    self.assertNotIn("Production Paid Alpha Plugin is installed", skill)
        self.assertEqual(archives[0], archives[1])
        self.assertEqual(before, source.read_bytes())

    def test_workflow_separates_build_from_trusted_signer(self):
        workflow = (candidate.ROOT / ".github/workflows/helper-build-spike.yml").read_text()
        for name in ("windows-test-build", "windows-test-native"):
            block = job_block(workflow, name)
            self.assertNotIn("secrets.", block)
            self.assertNotIn("environment:", block)
            self.assertIn("refs/heads/" + candidate.BRANCH, block)
        block = job_block(workflow, "windows-test-sign")
        for marker in (
            f"ref: {candidate.SIGNER_COMMIT}", "persist-credentials: false", "cache: false",
            "needs: windows-test-evidence",
            "artifact-ids: ${{ needs.windows-test-evidence.outputs.artifact_id }}",
            "merge-multiple: true",
            "run-id: ${{ inputs.build_run_id }}",
            "environment: paid-alpha-production-release", 'SIGN WINDOWS TEST $CANDIDATE_SHA',
            candidate.PROFILE.helper_version, candidate.PROFILE.signing_key_id,
            "candidate['descriptorSHA256'] == hashlib.sha256", "--descriptor candidate/helper/helper-release-descriptor.json",
        ):
            self.assertIn(marker, block)
        for forbidden in ("python3 tools/", "go test", "contents: write", "gh release", "run: candidate/", "cache: true"):
            self.assertNotIn(forbidden, block)
        self.assertIn("github.ref == 'refs/heads/main'", job_block(workflow, "signed-production-candidate"))
        native = job_block(workflow, "windows-test-native").splitlines()
        self.assertIn("go test -p 1 -count=1 -failfast", "\n".join(native))
        for index, line in enumerate(native):
            if line.strip().startswith(("go test ", "python tools/")):
                self.assertEqual(native[index + 1].strip(), "if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }")
        for name in ("windows-test-build", "windows-test-native", "macos-no-node-smoke"):
            self.assertNotIn("signed-windows-test-only", job_block(workflow, name))

    def test_reused_evidence_is_exact_successful_and_complete(self):
        run = {"id": 123, "head_sha": SHA, "repository": {"full_name": evidence.REPOSITORY},
               "head_repository": {"full_name": evidence.REPOSITORY}, "head_branch": evidence.BRANCH,
               "path": evidence.WORKFLOW, "event": "workflow_dispatch", "status": "completed",
               "conclusion": "success", "run_attempt": 1}
        jobs = {"total_count": 3, "jobs": [{"name": name, "status": "completed", "conclusion": "success",
                 "head_sha": SHA, "run_id": 123} for name in sorted(evidence.REQUIRED_JOBS)]}
        artifacts = {"total_count": 1, "artifacts": [{"name": f"windows-test-unsigned-{SHA}", "id": 456,
                     "expired": False, "workflow_run": {"id": 123, "head_sha": SHA}}]}
        self.assertEqual(evidence.select_artifact(run, jobs, artifacts, SHA, "123"), 456)
        for field, value in (("head_sha", "b" * 40), ("head_branch", "main"), ("conclusion", "failure"),
                             ("status", "in_progress"), ("event", "pull_request"), ("run_attempt", 2),
                             ("repository", {"full_name": "foreign/repo"}), ("path", "other.yml")):
            bad = copy.deepcopy(run)
            bad[field] = value
            with self.assertRaises(ValueError):
                evidence.select_artifact(bad, jobs, artifacts, SHA, "123")
        for mutation in ("skipped", "missing", "expired", "duplicate", "wrong-artifact-sha", "partial-jobs"):
            bad_jobs, bad_artifacts = copy.deepcopy(jobs), copy.deepcopy(artifacts)
            if mutation == "skipped":
                bad_jobs["jobs"][0]["conclusion"] = "skipped"
            elif mutation == "missing":
                bad_jobs["jobs"].pop()
                bad_jobs["total_count"] = 2
            elif mutation == "partial-jobs":
                bad_jobs["total_count"] = 101
            elif mutation == "expired":
                bad_artifacts["artifacts"][0]["expired"] = True
            elif mutation == "duplicate":
                bad_artifacts["artifacts"] *= 2
                bad_artifacts["total_count"] = 2
            else:
                bad_artifacts["artifacts"][0]["workflow_run"]["head_sha"] = "b" * 40
            with self.assertRaises(ValueError):
                evidence.select_artifact(run, bad_jobs, bad_artifacts, SHA, "123")


if __name__ == "__main__":
    unittest.main()
