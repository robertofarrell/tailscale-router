# Deferred upgrades

Changes worth making that were deliberately left out of the Go 1.27 /
Tailscale 1.102 / dnsproxy 0.84 version bump, so that bump stayed easy to
revert. Roughly in order of value.

## Guard the node key lookup

`main.go` reads its own node key with:

```go
output, err = exec.Command("bash", "-c", fmt.Sprintf("%s status --json | jq -r .Self.PublicKey", ...)).Output()
```

and then searches the tailnet's devices for a matching `nodeKey`. If that
lookup ever returns nothing — a renamed JSON field, a `tailscaled` that isn't
up yet, `jq` missing — `nodeKey` is empty, no device matches, and `selfID`
stays `""`. The route configuration then POSTs to
`/api/v2/device//routes`, which cannot succeed, and the machine keeps running
while advertising nothing.

This is the same silent-failure class as the auth key bug fixed in `f2ce5eb`:
the process exits 0, `start.sh` goes on to launch the DNS proxy, and the logs
say `fully configured`. Add an explicit check that `nodeKey` is non-empty and
that a device matched, and panic with a real message if not.

`Self.PublicKey` was confirmed present in Tailscale 1.102.3, so this is
defensive rather than a live bug.

## Pin the alpine base image

```dockerfile
FROM alpine:latest
```

The runtime stage tracks whatever `alpine:latest` points at, so two builds of
the same commit can produce different images — and an alpine major bump can
change `iptables` or `bind-tools` behaviour with no commit to point at. Pin a
specific `3.x` tag and bump it deliberately. The builder stage is already
pinned via `golang:1.27-alpine`.

## Stop shipping the source tree in the runtime image

The Dockerfile runs `COPY . ./` three times, twice in the final stage:

```dockerfile
FROM alpine:latest
...
COPY . ./                                                    # once
COPY --from=builder /app/tailscale-router /app/tailscale-router
COPY . ./                                                    # again, identical
```

The runtime image only needs `start.sh` and the built binary. Everything else —
`main.go`, `go.mod`, the `README` — is dead weight in the image, and the second
`COPY . ./` invalidates the layer cache for no benefit. Replace both with
`COPY start.sh ./`.

## Reconsider `tail -f /dev/null`

`start.sh` ends with `tail -f /dev/null` to hold the container open. That works,
but it means the machine stays "up" from Fly's perspective even if `tailscaled`
or `dnsproxy` has died. Fly restarts a machine whose main process exits, so
letting `dnsproxy` run in the foreground as the final command would turn a
crashed proxy into an automatic restart instead of a silently broken router.

## Known consequence of the 1.102 bump: connmark health warning

Tailscale 1.102 installs connmark rules that 1.30.2 did not, and Fly's kernel
has no `xt_connmark` module, so `tailscaled` reports a permanent `router`
health warning:

```
enabling connmark rules: ... in mangle/PREROUTING: exit status 2:
Warning: Extension CONNMARK revision 0 not supported, missing kernel module?
iptables v1.8.13 (nf_tables): unknown option "--nfmask"
```

This does not appear to stop the router working. After the upgrade the node
still reports `PrimaryRoutes: ["fdaa:0:c4b4::/48"]` and `Online: true`, the
IPv6 filter table has `FORWARD -j ts-forward` with all ten `ts-` chains
installed, and the failure is in the *IPv4* mangle table — `tailscaled` aborts
connmark setup there before it reaches IPv6, which is the only family this
router forwards.

What is unverified: an end-to-end `dig` through the proxy from a client on the
tailnet, as in the README's "Test it Out". Until someone runs that from a
device on the same tailnet, "the warning is harmless" is an inference from the
installed rules, not a measurement.

If it does turn out to matter, `tailscaled --netfilter-mode=nodivert` (leaving
the base rules in place but not the divert/mangle ones) is the first thing to
try.
