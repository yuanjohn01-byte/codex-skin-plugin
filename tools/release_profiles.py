#!/usr/bin/env python3
"""Fixed release-channel identities for immutable Helper/Bootstrap builds."""

from __future__ import annotations

from dataclasses import dataclass
from urllib.parse import urlsplit


@dataclass(frozen=True)
class ReleaseProfile:
    name: str
    helper_version: str
    bootstrap_version: str
    api_base_url: str

    @property
    def helper_release_tag(self) -> str:
        return f"helper-v{self.helper_version}"

    @property
    def api_host(self) -> str:
        return urlsplit(self.api_base_url).hostname or ""


STAGING = ReleaseProfile(
    name="staging",
    helper_version="0.1.0-paid-alpha.16",
    bootstrap_version="0.1.0-paid-alpha.15",
    api_base_url="https://codex-skin-staging.yuanjohn01.workers.dev",
)
PRODUCTION = ReleaseProfile(
    name="production",
    helper_version="0.1.0-paid-alpha.17",
    bootstrap_version="0.1.0-paid-alpha.16",
    api_base_url="https://codexskin.ai",
)
PROFILES = {profile.name: profile for profile in (STAGING, PRODUCTION)}


def profile_names() -> tuple[str, ...]:
    return tuple(PROFILES)


def release_profile(name: str) -> ReleaseProfile:
    try:
        return PROFILES[name]
    except KeyError as exc:
        raise ValueError(f"unknown release profile: {name}") from exc


def reject_unprofiled_protected_origin(api_base_url: str | None) -> None:
    hostname = urlsplit(api_base_url).hostname if api_base_url else None
    normalized = hostname.rstrip(".").lower() if hostname else None
    protected = {profile.api_host.rstrip(".").lower() for profile in PROFILES.values()}
    if normalized in protected:
        raise ValueError("protected Staging/Production origins require a fixed release profile")
