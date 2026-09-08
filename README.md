# tailscale-router

## Setup

Deploys are handled by the [Fly Deploy workflow](.github/workflows/fly-deploy.yml), which runs
`flyctl deploy` on every push to `main` and can also be triggered by hand from the Actions tab.
The steps below are one-time setup.

1. Fork or clone this repo.

2. Create the Fly app:

   ```bash
   flyctl apps create my-unique-tailscale-router-app-name
   ```

3. Tell CI which app to deploy to, by setting the app name as a repository variable
   (Settings > Secrets and variables > Actions > Variables), so it doesn't have to be
   committed to `fly.toml`:

   ```bash
   gh variable set FLY_APP_NAME --body my-unique-tailscale-router-app-name
   ```

   While you're in [`fly.toml`](fly.toml), set `primary_region` to a region near you
   (`flyctl platform regions` lists them).

4. Create the volume that holds `tailscaled`'s node identity. Its name must match
   `source` in [`fly.toml`](fly.toml)'s `[mounts]`, and its region must match
   `primary_region`:

   ```bash
   flyctl volumes create tailscale_state --size 1 --region syd -a my-unique-tailscale-router-app-name
   ```

   Without this the deploy fails, and without the mount the router would
   re-register as a new Tailscale node with a new IP on every deploy.

5. Get an **API key** from the Tailscale admin console: Settings > Keys > `Generate API key`.
   This is not the same thing as an auth key — the router mints its own ephemeral auth keys
   through the API, so it needs an API key to do that with.

6. Set the key as a Fly secret. The app reads it from `TAILSCALE_API_TOKEN`:

   ```bash
   flyctl secrets set TAILSCALE_API_TOKEN=thekeyyougot -a my-unique-tailscale-router-app-name
   ```

7. Give GitHub Actions a deploy token. Piping it straight into `gh` keeps the token out of your
   shell history, and scoping it to the one app means a compromised CI run can't touch your other
   Fly apps:

   ```bash
   flyctl tokens create deploy -a my-unique-tailscale-router-app-name | gh secret set FLY_API_TOKEN
   ```

8. Push to `main`. The workflow builds the image on Fly's remote builder and deploys a machine.

9. Check that your subnet routes are enabled in the Tailscale admin console under **Machines**.
   The router tries to enable them for itself over the API on startup, so this is usually just a
   verification step — see [Tailscale's subnet docs](https://tailscale.com/kb/1019/subnets/) if
   the routes show as pending approval.

10. Enjoy

## Deploying by hand

You don't need the workflow. With `flyctl` authenticated locally, pass the app name yourself —
`fly.toml` doesn't carry one:

```bash
flyctl deploy --remote-only -a my-unique-tailscale-router-app-name
```

## Test it Out

Two separate things can be broken here, so test them in order. Both need the
device you are testing from to be on the **same tailnet** the router joined.

First, find the router's Tailscale IP — this is the `100.x` address, not the
`fdaa:` one that `flyctl m list` prints:

```bash
flyctl ssh console -a my-unique-tailscale-router-app-name -C '/app/tailscale ip -4'
```

**1. Is the DNS proxy reachable?** Query it directly by IP, which needs only
plain tailnet connectivity:

```bash
dig @100.x.x.x aaaa my-unique-tailscale-router-app-name.internal
```

Expect `status: NOERROR` and an `fdaa:` address in the ANSWER section. A
timeout means the proxy is not reachable — check the app is running and that
`dnsproxy` appears in `flyctl logs`.

**2. Is the subnet route working?** Reach the address that came back. This is
the part that depends on the advertised route, not just DNS:

```bash
# the fdaa: address dig returned above
ping6 fdaa:0:c4b4:a7b:e2:b785:6b05:2
```

If step 1 answers but step 2 does not, DNS is fine and the route is the
problem: check the routes are approved in the Tailscale admin console under
**Machines**.

## DNS Setup

The steps above query the proxy explicitly with `dig @...`. Split DNS makes
`*.internal` names resolve through it automatically, so a plain
`curl http://some-app.internal:8080` works without naming a DNS server.
See [Tailscale's DNS docs](https://tailscale.com/kb/1054/dns/) for the
underlying feature.

1. Get the router's Tailscale IP:

   ```bash
   flyctl ssh console -a my-unique-tailscale-router-app-name -C '/app/tailscale ip -4'
   ```

2. In the Tailscale admin console, open **DNS**, and under **Nameservers**
   choose **Add nameserver** > **Custom**.

3. Paste the `100.x` address from step 1 as the nameserver.

4. Turn on **Restrict to search domain** and enter `internal` as the search
   domain. Without this, *all* your DNS goes through this one Fly machine
   rather than just `*.internal` names.

5. Save, then confirm resolution now works with no `@server` argument:

   ```bash
   dig aaaa my-unique-tailscale-router-app-name.internal
   curl http://some-fly-app.internal:8080   # use the app's internal_port
   ```

This entry pins one IP address, so it breaks if the router's Tailscale IP ever
changes. The volume keeps that IP stable across deploys, but losing the volume
re-registers the node with a new address — see [NOTES.md](NOTES.md) for what
that looks like and how to recover.
