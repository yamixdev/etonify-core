# Etonify core

This branch keeps Etonify-specific Android integration on top of an exact
stable `sing-box` release tag. Upstream changes and Etonify changes must remain in
separate commits so the fork can be rebased and audited without copying the
legacy core wholesale.

## Stable baseline

| Input | Pinned value |
| --- | --- |
| Upstream repository | `https://github.com/SagerNet/sing-box.git` |
| Upstream branch | `testing` |
| Upstream commit | `0b8995879f29a9b98ee027bc17b75e101445b238` |
| Exact release tag | `v1.14.0` |
| Go toolchain used by Etonify Android CI | `1.26.7` |
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

## Mobile XHTTP client profile

The mobile core accepts `xhttp` and the legacy `splithttp` alias for outbound
V2Ray transports. The implementation is deliberately client-only and supports
`packet-up`, `stream-up`, and `stream-one`; it does not add an inbound XHTTP
server or HTTP/3 transport to the Android library. In `auto` mode Reality uses
`stream-one`, regular TLS/H2 uses `stream-up`, and cleartext HTTP uses
`packet-up`.

Mobile resource limits are part of the capability contract:

- XMUX has a hard limit of 16 physical HTTP clients. An all-zero XMUX block
  receives bounded concurrency, request-count, and lifetime defaults instead
  of creating an unlimited pool.
- Packet uploads use at most 256 KiB per logical stream and each finite upload
  request has a 30-second deadline.
- HTTP/2 health checks use a 30-second mobile default. Explicit keep-alive
  periods are bounded to 5 seconds through 5 minutes, and `-1` disables them.
- Client shutdown or network reset cancels active logical streams, closes
  response bodies, releases XMUX leases, and retires idle HTTP transports.
  Redirects are rejected so session metadata is never forwarded to another
  origin.

The capability document reports the exact profile and limits. New Xray XHTTP
extensions must stay disabled until they have their own compatibility and
resource regression tests.

## Opt-in VLESS post-quantum encryption

VLESS outbounds accept the Xray-compatible `encryption` string for
`mlkem768x25519plus`. The default (`""` or `"none"`) continues to use the
upstream `sing-vmess` client unchanged, so existing subscriptions do not create
encryption state or pay a runtime memory cost.

The wire format follows the upstream Xray implementation in
<https://github.com/XTLS/Xray-core/tree/main/proxy/vless/encryption> without
vendoring Xray or replacing the `sing-vmess` module.

The mobile implementation is outbound-only and supports:

- `1rtt` and cached `0rtt` handshakes;
- `native`, `xorpub`, and `random` wire modes;
- X25519 and ML-KEM-768 relay public keys;
- AES-GCM on supported hardware and ChaCha20-Poly1305 elsewhere;
- TCP, UDP, XUDP, Vision, and the configured V2Ray client transport. Vision
  remains above the encrypted record layer and cannot bypass it.

Resource and failure bounds are part of the contract: at most eight relay
keys, a 16 KiB configuration string, 16 padding segments, five seconds per
padding gap, ten seconds across all configured gaps, and a 12-second handshake
deadline. Cancellation closes transports that cannot implement native
deadlines. The core does not include a VLESS encryption inbound/server.

Interoperability coverage performs real client/server exchanges for every wire
mode with both AEAD choices, X25519 and ML-KEM relay keys, cached 0-RTT,
concurrent ticket use, cancellation, and corrupted/invalid configuration. The
race suite must pass before this capability is published in an Android AAR.
