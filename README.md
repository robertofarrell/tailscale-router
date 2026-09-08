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

4. Get an **API key** from the Tailscale admin console: Settings > Keys > `Generate API key`.
   This is not the same thing as an auth key — the router mints its own ephemeral auth keys
   through the API, so it needs an API key to do that with.

5. Set the key as a Fly secret. The app reads it from `TAILSCALE_API_TOKEN`:

   ```bash
   flyctl secrets set TAILSCALE_API_TOKEN=thekeyyougot -a my-unique-tailscale-router-app-name
   ```

6. Give GitHub Actions a deploy token. Piping it straight into `gh` keeps the token out of your
   shell history, and scoping it to the one app means a compromised CI run can't touch your other
   Fly apps:

   ```bash
   flyctl tokens create deploy -a my-unique-tailscale-router-app-name | gh secret set FLY_API_TOKEN
   ```

7. Push to `main`. The workflow builds the image on Fly's remote builder and deploys a machine.

8. Check that your subnet routes are enabled in the Tailscale admin console under **Machines**.
   The router tries to enable them for itself over the API on startup, so this is usually just a
   verification step — see [Tailscale's subnet docs](https://tailscale.com/kb/1019/subnets/) if
   the routes show as pending approval.

9. Enjoy

## Deploying by hand

You don't need the workflow. With `flyctl` authenticated locally, pass the app name yourself —
`fly.toml` doesn't carry one:

```bash
flyctl deploy --remote-only -a my-unique-tailscale-router-app-name
```

## Test it Out

You can test if it's working by finding the IP address of your new Fly.io app and using `dig`:

```bash
# Get the IP address of your app:
flyctl m list -a my-unique-tailscale-router-app-name

# Use dig to test DNS queries the DNS proxy setup in this repository
dig @<your-app-ip-address-here> aaaa my-unique-tailscale-router-app-name.internal
```

## DNS Setup

You can enable split DNS in your Tailscale settings to automatically resolve `*.internal` addresses through the DNS proxy setup in your new Fly.io app.

Tailscale documentation for that is [found here](https://tailscale.com/kb/1054/dns/).

1. Add a nameserver
2. Use the IP address of your new Fly.io app
3. Restrict to search domains, and use search domain `internal`

Then addresses should resolve! Maybe use `curl` to make an HTTP request to one of your apps. Be sure to use the `internal_port` of your application:

```bash
curl http://some-fly-app.internal:8080
```
