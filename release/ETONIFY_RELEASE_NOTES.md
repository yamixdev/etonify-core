## Etonify Core 1.14 RC test build

This is an Android integration build for testing Etonify with the sing-box `1.14.0-rc.1` baseline plus selected Android-relevant fixes backported through `1.14.0-rc.5`. It is not an APK and is not ready for production use.

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
- Naive HTTPS/QUIC forwards receive-window settings and closes connections cleanly after a network change.
- Naive QUIC uses the corrected congestion-control implementation and dependency set from sing-box 1.14.0-rc.2.
- Non-empty outbound groups remain visible even when they contain only one proxy.
- WebSocket early data works correctly with smux and yamux multiplexing.
- System-stack TCP NAT avoids cross-family port reuse through sing-tun v0.9.0-beta.3.
- Snell fuses the client handshake with the first payload.
- HTTP/2 transport reset remains compatible with Go 1.27 while current builds use Go 1.26.7.
- URLTest resolves nested outbound groups recursively without dropping Etonify status and error details.
- Long-lived TUIC and Naive QUIC connections retain accurate congestion-control samples after idle periods.
- boxdd starts its log factory before publishing the standard logger.

### Automated verification

- Full Go test suite on Linux.
- Android configuration compatibility corpus for Etonify-generated configurations.
- Race-detector coverage for runtime shutdown, URLTest, VLESS and XHTTP paths.
- Resource and performance regression gates.
- Reproducible Android AAR build with pinned Go, gomobile, Java, Android NDK, API level and build tags.
- SHA-256 checksum, source archive, source commit and machine-readable provenance.

### Device validation still required

Before replacing the bundled production core, test VPN and local proxy modes, TCP and UDP traffic, system/gVisor/mixed TUN stacks, Wi-Fi/LTE handoff, DNS modes, routing rule-sets, targeted and group URLTest, external IP lookup, repeated start/stop cycles and an application update without clearing data.
