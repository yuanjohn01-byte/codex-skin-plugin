#!/usr/bin/env python3
"""Select one immutable artifact from a successful exact-SHA Windows test run."""

import json
import re
import sys


REPOSITORY = "yuanjohn01-byte/codex-skin-plugin"
BRANCH = "codex/win-002-test-channel"
WORKFLOW = ".github/workflows/helper-build-spike.yml"
REQUIRED_JOBS = {"windows-test-build", "windows-test-native", "macos-no-node-smoke"}


def select_artifact(run, jobs, artifacts, sha, run_id):
    if not re.fullmatch(r"[0-9a-f]{40}", sha) or not re.fullmatch(r"[1-9][0-9]*", run_id):
        raise ValueError("exact candidate SHA and numeric build run ID required")
    if (run.get("id") != int(run_id) or run.get("head_sha") != sha
            or run.get("repository", {}).get("full_name") != REPOSITORY
            or run.get("head_repository", {}).get("full_name") != REPOSITORY
            or run.get("head_branch") != BRANCH or run.get("path") != WORKFLOW
            or run.get("event") != "workflow_dispatch" or run.get("status") != "completed"
            or run.get("conclusion") != "success" or run.get("run_attempt") != 1):
        raise ValueError("build evidence is not a completed first-attempt exact-SHA channel run")
    if jobs.get("total_count") != len(jobs.get("jobs", [])):
        raise ValueError("incomplete job list")
    for name in REQUIRED_JOBS:
        matches = [job for job in jobs["jobs"] if job.get("name") == name]
        if (len(matches) != 1 or matches[0].get("conclusion") != "success"
                or matches[0].get("status") != "completed"
                or matches[0].get("head_sha") != sha or matches[0].get("run_id") != int(run_id)):
            raise ValueError("required candidate/platform check is missing or unsuccessful")
    if artifacts.get("total_count") != len(artifacts.get("artifacts", [])):
        raise ValueError("incomplete artifact list")
    matches = [item for item in artifacts["artifacts"] if item.get("name") == f"windows-test-unsigned-{sha}"]
    if len(matches) != 1:
        raise ValueError("expected one exact candidate artifact")
    item = matches[0]
    if (item.get("expired") is not False or not isinstance(item.get("id"), int)
            or item["id"] < 1 or item.get("workflow_run", {}).get("id") != int(run_id)
            or item.get("workflow_run", {}).get("head_sha") != sha):
        raise ValueError("candidate artifact is expired or has different provenance")
    return item["id"]


if __name__ == "__main__":
    try:
        if len(sys.argv) != 3:
            raise ValueError("usage: verify_windows_test_run.py SHA RUN_ID")
        with open("build-run.json") as handle:
            run = json.load(handle)
        with open("build-jobs.json") as handle:
            jobs = json.load(handle)
        with open("build-artifacts.json") as handle:
            artifacts = json.load(handle)
        print(f"artifact_id={select_artifact(run, jobs, artifacts, *sys.argv[1:])}")
    except (OSError, ValueError, KeyError, TypeError) as exc:
        sys.exit(f"Windows test evidence rejected: {exc}")
