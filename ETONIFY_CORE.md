# Etonify core

This branch keeps Etonify-specific Android integration on top of an exact
`sing-box` release-candidate tag. Upstream changes and Etonify changes must remain in
separate commits so the fork can be rebased and audited without copying the
legacy core wholesale.

## Stable baseline

| Input | Pinned value |
| --- | --- |
| Upstream repository | `https://github.com/SagerNet/sing-box.git` |
| Upstream branch | `testing` |
| Upstream commit | `8dd67a1e49711ce8a9a884bef60a2139ef36446f` |
| Exact release tag | `v1.14.0-rc.1` |
| Go toolchain used by upstream Android CI | `1.26.6` |
| gomobile / gobind | `v0.1.13` |
| Android NDK | `r28` |
| OpenJDK | `17` |
| Main libbox Android API | `24` |

The Etonify Android app currently has `minSdk 26`. Keeping the library's
minimum API at 24 does not re-enable older Android versions in the app; it only
keeps the upstream libbox build compatible with the app's higher minimum API.

## Baseline verification

Run the Go checks before adding Etonify functionality:

```sh
go test ./...
```

The upstream WinDivert and TLS-spoof integration tests require access to the
Windows Service Manager. They can fail with `Access is denied` in a non-elevated
local shell; Android release verification therefore runs the complete suite on
Linux, while local Windows checks still cover the platform-independent core
packages.

Build the upstream Android libraries with the pinned JDK and NDK available:

```sh
make lib_install
make lib_android
```

The build produces `libbox.aar` and `libbox-legacy.aar` in the repository root.
It also emits matching generated Java source JARs. Etonify consumes only
`libbox.aar` and `libbox-sources.jar`; the legacy artifacts are retained at this
stage only because the unmodified upstream build command produces them.

The `Etonify libbox` workflow runs the complete Go package suite on Linux,
builds with pinned tools, and uploads the main AAR together with its source JAR,
SHA-256, source archive, and build provenance. A workflow artifact is not a
release: it must still pass the client migration and device test matrix before
it can replace the bundled AAR.

## Fork policy

- Do not modify subscription or user-setting storage in this repository.
- Add mobile-facing behavior through versioned libbox capabilities.
- Keep targeted probes, group probes, external IP lookup, and runtime cleanup
  in separate commits with regression tests.
- Do not copy the old core's `third_party` tree or dependency forks.
- Do not publish an AAR without its source commit, build inputs, and SHA-256.
- Do not replace the client AAR until the client migration and rollback tests
  pass against an in-place upgrade from the current Etonify release.

## Mobile capability contract

`libbox.EtonifyCapabilities()` returns a versioned JSON document. Clients must
fall back to the bundled legacy capability set when the method is absent or the
document is malformed. A capability may be enabled only in the same commit as
its implementation and regression coverage.

The first Etonify runtime extension on the 1.14 base provides managed URLTest
sessions and outbound external-address lookup. URLTest requests may target one
member or a complete group, bound per-probe and whole-session deadlines, limit
parallel work, replace stale forced sessions, and report a stable error code to
the client. External-address lookups use the selected concrete outbound,
bounded response sizes, a second provider, request coalescing, and a short
stale cache so a transient provider failure does not blank already known data.

Managed probe sessions also refresh URLTest selections after their final
result. Failed real dials invalidate only the affected outbound and immediately
recalculate the group from known healthy history; unavailable/error-only
entries can no longer win simply because they contain a history timestamp.
