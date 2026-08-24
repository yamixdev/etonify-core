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
| gomobile / gobind | `v0.1.12` |
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
Etonify consumes only the main `libbox.aar`; the legacy artifact is retained at
this stage only because the unmodified upstream build command produces it.

## Fork policy

- Do not modify subscription or user-setting storage in this repository.
- Add mobile-facing behavior through versioned libbox capabilities.
- Keep targeted probes, group probes, external IP lookup, and runtime cleanup
  in separate commits with regression tests.
- Do not copy the old core's `third_party` tree or dependency forks.
- Do not publish an AAR without its source commit, build inputs, and SHA-256.
- Do not replace the client AAR until the client migration and rollback tests
  pass against an in-place upgrade from the current Etonify release.
