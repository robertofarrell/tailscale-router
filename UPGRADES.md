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
