# Archives, and what is not backed up

```bash
sudo ratline backup acme                       # one tenant's home
sudo ratline backup app.example.com            # one site directory
sudo ratline backup acme --out /mnt/nas
```

## What `backup` actually does

It archives **one tenant's home, or one site directory** — and that is the whole of
it. The archive holds the application code, the logs and the `.env`.

Because it holds the `.env`, **it holds secrets**. It is written `0600` inside a `0700`
directory, and where it goes afterwards is your responsibility.

`ratline site delete` writes one of these automatically unless `--purge` says
otherwise, so the archive of a site you removed by mistake already exists.

## What it does not do

`backup` is not a server backup. It does **not** include:

* the state database at `/var/lib/ratline/state.db`
* `/etc/ratline` — configuration, global keys, the revocation list, DNS credentials
* the generated nginx vhosts or systemd units
* the certificates

And there is **no `ratline restore`**. Nothing unpacks an archive back into place.

That is a real gap, stated plainly rather than implied: if you rely on this server,
back up `/var/lib/ratline/state.db` and `/etc/ratline` with whatever already backs up
the rest of the host. Those two are what a rebuild needs, because the nginx and
systemd configuration can be regenerated from state and the application code comes
from your repository.

## Rebuilding a server by hand

```bash
sudo ratline init
# restore /var/lib/ratline/state.db and /etc/ratline from your host backup
sudo ratline reconcile --fix     # regenerate the vhosts and units from state
sudo ratline doctor
sudo ratline troubleshoot        # the host: clock, disk, tooling, state
```

`reconcile --fix` regenerates everything derivable from state. `doctor` then reports
what still needs attention — most often certificates whose DNS now points elsewhere,
and application code that has not been deployed yet.

## Exporting state

```bash
sudo ratline export > inventory.json
```

A JSON dump of everything ratline knows, carrying **no private key material at all** by
design — so it is safe to hand to a monitoring system or a configuration-management
tool. It is a record, not a restore path.
