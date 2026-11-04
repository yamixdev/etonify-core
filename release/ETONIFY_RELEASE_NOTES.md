## Etonify Core 1.15 alpha test base

### 1.15.0-alpha.10-etonify.6

- Remove Etonify's standalone root daemon and its routing-ownership hooks. Upstream platform support is unchanged.
- Backport upstream `1a55e9e` (forward endpoint local flows to TUN) and `9f1cd1ef` (close UDP sockets when device addresses change).
- Backport routed-port support into the pinned sing-tun fork while retaining the NAT, reset, fragment and async-close regression fixes.
- Apply URLTest exclusions to a running instance without interrupting attached connections. Cancel old checks and reject stale results and requests from a replaced runtime.
- Initialize exclusions before group startup so excluded members cannot be selected during the client handshake.

### 1.15.0-alpha.10-etonify.4

- Add the optional standalone Android arm64 root daemon with signed module delivery, restricted app-UID RPC access and owned routing recovery.
- Reserve routing priorities before Android explicit-network rules without deleting the system legacy routing rule.
- Keep root routing ownership opt-in; ordinary Android VPN continues to use its existing lifecycle.
- Root mode remains experimental and requires device verification of routing, SELinux and cleanup.

### 1.15.0-alpha.10-etonify.3

- Integrate the forward NAT/fragment backport using the pinned `yamixdev/sing-tun` fork, not a cached upstream module.
- Fork source: `3692459a3211ad692b83580cd4c95e1ff4e20080`; upstream base: `5c2edb183cc92eb190d41c04dedfa5519ca3e78c`.
- Make dispatcher teardown single-owner, including concurrent Close and registration during shutdown.
- Track unique fragment coverage in a bounded bitmap; preserve missing fragments after duplicates/overlaps and keep fixed expiry.
- Quarantine released UDP selectors for four minutes across resets and reacquisition of the same live Port. Free physical reservations on expiry even without traffic; serialize final release before new ownership.
- Guard active/retired cleanup timers by generation. Registry capacity is reserved for at most 1024 active plus retired allocators; pressure refuses unsafe reuse. This finite window cannot identify arbitrarily late UDP replies after reuse.
- Add real packet, shared-owner, expiry, exhaustion and race regressions; preserve existing mapping/filtering modes, libbox API, Android API24 and ARM build tags.
- Include pinned fork source/license in the source archive and record/check fork repository, SHA, upstream base and Go module version in both AAR provenance paths.
- Local targeted/race/configuration/source-provenance checks pass. Full Windows integration suites still require driver privileges/IPv6 and contain upstream vet failures. Remote Linux CI/AAR and phone acceptance are pending delivery; the bundled client AAR is not changed by this source revision.
- Known minor: source archive bytes depend on Git checkout line-ending configuration. Repeated archives in the same configuration are deterministic; cross-platform normalization is deferred.

### 1.15.0-alpha.10-etonify.2

- Backport upstream scope cleanup filtering for expected cancellation and closed-resource errors.
- Preserve genuine failures in wrapped mixed error trees and retain their diagnostic context.
- Keep Etonify's late-resource cleanup and concurrent startup/shutdown protections.

### 1.15.0-alpha.10-etonify.1

- Rebase the test baseline to the exact upstream `v1.15.0-alpha.10` release.
- Adapt Etonify runtime cancellation, network binding and managed URLTest to the upstream scoped lifecycle.
- Preserve Etonify XHTTP/REALITY, VLESS encryption, external IP lookup and versioned Android capabilities.
- Keep Android API 24 and ARM-only builds without gVisor or unused VPN protocol integrations.
- Verify Go tests, race checks and Android AAR exclusively in GitHub Actions. Device validation remains required.

### 1.15.0-alpha.3-etonify.5

- Verify full NaiveProxy (`with_naive_outbound`) and AnyTLS compilation in core builds.
- Sanitize NaiveProxy Cronet TLS configurations, rejecting incompatible TLS fields to prevent startup crashes.
- Support AnyTLS `client_metadata`, `idle_session_check_interval`, `idle_session_timeout`, and `min_idle_session`.

### 1.15.0-alpha.3-etonify.4

- Fix full URLTest deadline truncations and responsive progress tracking during concurrent sweeps.
- Protect background URLTest from premature timeouts across large proxy catalogs.

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

This Android library is built from the prerelease sing-box `1.15.0-alpha.10` tag with Etonify's mobile integration applied on top. It is a test core artifact, not an APK, and must pass device validation before production use.

### Included

- The sing-box 1.15.0-alpha.10 networking, DNS, routing, TUN, QUIC and Android baseline.
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
