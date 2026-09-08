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

This has since been measured from a client on the same tailnet, on the
upgraded build. Both paths work:

```
$ dig @100.74.136.13 aaaa personal-tailscale-router.internal
;; ->>HEADER<<- opcode: QUERY, status: NOERROR
personal-tailscale-router.internal. 10 IN AAAA fdaa:0:c4b4:a7b:e2:b785:6b05:2
;; Query time: 24 msec

$ ping6 -c 3 fdaa:0:c4b4:a7b:e2:b785:6b05:2
3 packets transmitted, 3 packets received, 0.0% packet loss
```

The `/48` is installed on the client as a route via the Tailscale interface, so
the ping traverses the advertised subnet route rather than going direct.

Still untested: forwarding to a *third* host inside the 6PN. The ping target is
the router machine itself, because it is currently the only app in the org, so
route acceptance and delivery are proven but transit to another host is not.

If it does turn out to matter, `tailscaled --netfilter-mode=nodivert` (leaving
the base rules in place but not the divert/mangle ones) is the first thing to
try.

## The router's tailnet IP changes on every deploy

`tailscaled` keeps its node identity in `--state=/var/lib/tailscale/tailscaled.state`,
which lives in the container filesystem and is therefore replaced on every
deploy. `main.go` then mints a fresh *ephemeral* auth key and `tailscale up`
registers as a brand new node, with a new `100.x` address:

```
before deploy: 100.74.136.13
after deploy:  100.117.9.99   # same Fly machine, new tailnet node
```

This matters because the README's "DNS Setup" section has you enter that IP as
a nameserver in Tailscale's split DNS config. That entry goes stale on every
deploy, and `dig` against the old address times out until it is updated by
hand. It also leaves a trail of `offline` ephemeral nodes in the tailnet.

Options, roughly in order of preference:

1. Attach a Fly volume for `/var/lib/tailscale` so the node identity survives a
   deploy. Keeps one stable node and one stable IP.
2. Drop `ephemeral` from the key capabilities in `main.go` and pass a fixed
   `--hostname`, so re-registration reuses the existing node rather than
   creating a sibling.
3. Leave it, and treat the split-DNS nameserver entry as something to update
   after each deploy. Only reasonable if deploys are rare.

Until one of these lands, the README's DNS Setup steps should be read as
"re-do this after every deploy".
