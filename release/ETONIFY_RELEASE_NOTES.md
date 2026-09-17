## Etonify Core 1.15 alpha test base

### 1.15.0-alpha.3-etonify.3

- Add `certificate_sha256` outbound TLS option for whole-certificate SHA256 pinning (Xray `pinnedPeerCertSha256` parity).
- Support leaf certificate pinning and intermediate/root CA pinning across standard Go TLS, uTLS, Windows and Apple TLS engines.
- Add flexible parsing for hex with colons/spaces/hyphens, plain hex, and Base64 certificate hashes.

### 1.15.0-alpha.3-etonify.2

- Stream progressive URLTest results to the client as each proxy check completes.
- Support concurrent targeted/manual URLTest checks during running full sweeps without being swallowed.
- Enhance external IP lookup resilience with direct 1.1.1.1 query, domain fallback, and pure IPv4 ipify.
- Fix duplicate DNS queries bypassing deduplication after failed exchange (`exchangePending` async pipeline).
- Bind network reset dispatch to manager lifecycle, fixing interface monitor teardown races on network switches.
- Update `bbolt` to eliminate crash on corrupted cache database files.
- Update `sing` and `sing-tun` with closed connection handling on Windows and iptables auto-redirect DNS hijack fixes.
- Prevent memory pressure callbacks from restarting stopped OOM killer timers on Darwin.

### 1.15.0-alpha.3-etonify.1

- Use the new sing-tun TCP/IP stack by default. The legacy system, gVisor and mixed stacks remain available as compatibility modes.
- Coalesce repeated network-environment updates before performing the more expensive refresh work.
- Track active outbound and DNS references, and close idle resources that are no longer used by routing or the selected proxy group.
- Preserve Etonify's bounded URLTest sessions, XHTTP lifecycle fixes and Android network binding behavior on the 1.15 codebase.

This Android library is built from the prerelease sing-box `1.15.0-alpha.3` tag with Etonify's mobile integration applied on top. It is a test core artifact, not an APK, and must pass device validation before production use.

### Included

- The sing-box 1.15.0-alpha.3 networking, DNS, routing, TUN, QUIC and Android baseline.
- The new sing-tun TCP/IP stack selected by omitting the deprecated `stack` field.
- Reference-aware idle connection management for outbounds and DNS transports.
- Versioned Etonify capabilities so the client enables only features implemented by this core.
- Targeted and group URLTest with bounded parallelism, cancellation, structured errors and failover.
- External IP and country lookup through the selected outbound.
- Bounded subscription and resource downloads through the selected outbound.
- Reset-safe bounded XHTTP/SplitHTTP transport for network changes.
- Optional VLESS Encryption with Vision compatibility.
- Reality `spider_x` fallback support.
- Deterministic runtime shutdown and file-descriptor ownership fixes.
- Selector connection interruption during outbound changes.
- Android libbox excludes WireGuard, Tailscale, OpenVPN, OpenConnect and USB/IP features that the Etonify client does not expose.

### Automated verification

- Full Go test suite on Linux.
- Android configuration compatibility corpus for Etonify-generated configurations.
- Race-detector coverage for runtime shutdown, URLTest, VLESS and XHTTP paths.
- Resource and performance regression gates.
- Reproducible Android AAR build with pinned Go, gomobile, Java, Android NDK, API level and build tags.
- SHA-256 checksum, source archive, source commit and machine-readable provenance.

### Device validation still required

Before production use, test VPN and local proxy modes, TCP and UDP traffic, the native stack and every compatibility stack, Wi-Fi/LTE handoff, DNS modes, routing rule-sets, targeted and group URLTest, external IP lookup, repeated start/stop cycles and an application update without clearing data.
