# Operational notes

Things worth knowing when running this app that are not obvious from the code.

## Losing the volume leaves a duplicate Tailscale node behind

The minted auth key is deliberately **not** ephemeral (`main.go`), because
Tailscale reaps ephemeral nodes once they go offline and that would invalidate
the identity persisted on the `tailscale_state` volume between deploys.

The trade-off is that nodes now outlive the machine. If the volume is lost or
recreated, `tailscaled` starts with no state and registers afresh — but the old
node is still there, so Tailscale keeps the name unique by appending a counter:

```
personal-tailscale-router      # the old node, offline forever
personal-tailscale-router-1    # the new registration
```

The router works fine as `-1`, but the stale node stays in the tailnet until
someone removes it, and the new node has a different Tailscale IP, which stales
any split-DNS nameserver entry pointing at the old one.

When that happens:

1. Delete the old node in the Tailscale admin console under **Machines**.
2. Read the new IP with
   `flyctl ssh console -a <app> -C '/app/tailscale ip -4'`.
3. Update the split-DNS nameserver entry described in the README.

Before the volume existed this happened on *every* deploy, with ephemeral nodes
being reaped automatically. Now it should only happen on volume loss.
