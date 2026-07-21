#!/usr/bin/env python3
"""Prevent duplicate feature-push and pull-request workflow executions."""

from __future__ import annotations

from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
WORKFLOWS = ROOT / ".github" / "workflows"
BASELINE = WORKFLOWS / "ci.yml"


def main() -> int:
    workflow_paths = sorted(WORKFLOWS.glob("*.yml"))
    if not workflow_paths:
        raise AssertionError("no Public workflows found")

    for path in workflow_paths:
        workflow = path.read_text()
        if "codex/**" in workflow:
            raise AssertionError(f"{path.name} restores duplicate feature push CI")
        for marker in ("pull_request:", "workflow_dispatch:", "concurrency:"):
            if marker not in workflow:
                raise AssertionError(f"{path.name} is missing {marker}")
        if "cancel-in-progress: true" not in workflow:
            raise AssertionError(f"{path.name} does not cancel stale runs")

    baseline = BASELINE.read_text()
    if "  push:" not in baseline or "      - main" not in baseline:
        raise AssertionError("Public baseline must verify merged main")

    print("Public CI workflow tests passed (PR/main only; stale runs cancel).")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
