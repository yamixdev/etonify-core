## Etonify Core 1.14 stable base

This Android library is built from the final sing-box `1.14.0` release with Etonify's mobile integration applied on top. It is a core artifact, not an APK. Device validation is still required before it replaces the library bundled with the production application.

### Included

- The complete sing-box 1.14.0 networking, DNS, routing, TUN, QUIC and Android baseline.
- Stable 1.14 DNS timeouts, optimistic caching, corrected rule-set matching and network reset behavior.
- Updated quic-go, gVisor, uTLS, Tailscale and NaiveProxy dependency set from the final upstream release.
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

Before replacing the bundled production core, test VPN and local proxy modes, TCP and UDP traffic, system/gVisor/mixed TUN stacks, Wi-Fi/LTE handoff, DNS modes, routing rule-sets, targeted and group URLTest, external IP lookup, repeated start/stop cycles and an application update without clearing data.
