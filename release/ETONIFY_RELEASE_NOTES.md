## Etonify Core 1.14 RC test build

This is an Android integration build for testing Etonify with the sing-box `1.14.0-rc.1` baseline. It is not an APK and is not ready for production use.

### Included

- Current sing-box 1.14 networking, DNS, routing, TUN, QUIC and Android fixes.
- Versioned Etonify capabilities so the client enables only features implemented by this core.
- Targeted and group URLTest with bounded parallelism, cancellation, structured errors and failover.
- External IP and country lookup through the selected outbound.
- Bounded subscription and resource downloads through the selected outbound.
- Reset-safe bounded XHTTP/SplitHTTP transport for network changes.
- Optional VLESS Encryption with Vision compatibility.
- Reality `spider_x` fallback support.
- Deterministic runtime shutdown and file-descriptor ownership fixes.
- Selector connection interruption and WireGuard start/stop race protection.
- Android libbox no longer bundles unused Tailscale functionality.

### Automated verification

- Full Go test suite on Linux.
- Android configuration compatibility corpus for Etonify-generated configurations.
- Race-detector coverage for runtime shutdown, URLTest, VLESS and XHTTP paths.
- Resource and performance regression gates.
- Reproducible Android AAR build with pinned Go, gomobile, Java, Android NDK, API level and build tags.
- SHA-256 checksum, source archive, source commit and machine-readable provenance.

### Device validation still required

Before replacing the bundled production core, test VPN and local proxy modes, TCP and UDP traffic, system/gVisor/mixed TUN stacks, Wi-Fi/LTE handoff, DNS modes, routing rule-sets, targeted and group URLTest, external IP lookup, repeated start/stop cycles and an application update without clearing data.
