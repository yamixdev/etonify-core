# Etonify Core 1.14 Android test checklist

Record the APK/core commit, device, Android version and network provider for
each run. Compare resource measurements with the pinned 1.13.19 build on the
same device.

## Lifecycle

- [ ] Cold start and first VPN permission flow.
- [ ] Repeated start/stop, including stop while the core is starting.
- [ ] Restart after DNS, routing, AdBlock and local proxy changes.
- [ ] Remove the Flutter UI from recents while the VPN service keeps running.
- [ ] Reopen the UI and confirm it reads the native service state.
- [ ] Screen off for at least one hour, then verify traffic and notification.

## Network changes

- [ ] Wi-Fi to LTE to Wi-Fi without a manual VPN restart.
- [ ] Wi-Fi A to Wi-Fi B.
- [ ] Lose all networks, show offline state, then recover automatically.
- [ ] Verify DNS, UDP, QUIC, Telegram calls and long-lived TCP after handoff.

## Protocol and configuration corpus

- [ ] CI builds the Android AAR with `with_naive_outbound`; do not substitute
      the platform-specific Android Cronet link with a Linux Cronet test.
- [ ] VLESS TLS, Reality/Vision, XHTTP/SplitHTTP and Encryption.
- [ ] VMess WS/gRPC/HTTPUpgrade, Trojan and Shadowsocks.
- [ ] Hysteria2, TUIC, AnyTLS and Naive.
- [ ] WireGuard endpoint with a hostname and UDP after network handoff.
- [ ] TUN system/gVisor/mixed, local proxy and combined mode.
- [ ] FakeIP, DoH/DoT/UDP/TCP/device DNS, AdBlock and traffic presets.
- [ ] Proxy chains and profiles imported from 0.2.1, 0.2.5 and 0.3.0.

## Runtime APIs

- [ ] Targeted URLTest checks only the selected concrete outbound.
- [ ] Group URLTest reports progressive results without stale overwrites.
- [ ] External IP/country resolves and cancels cleanly on stop/reset.
- [ ] `lowest` changes only after a confirmed result or connection failure.

## Resource comparison

- [ ] Record AAR size and runtime cold-start/stop/restart times.
- [ ] Record PSS/RSS/Private Dirty at 5, 30 and 120 minutes.
- [ ] Record idle/URLTest CPU, file descriptors and goroutines.
- [ ] Confirm no unexplained growth beyond the limits in
      `ETONIFY_PERFORMANCE_BASELINE`.
