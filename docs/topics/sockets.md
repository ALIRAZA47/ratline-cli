# Sockets, ports and the silent 502

> Why a node or python site listens on a Unix socket, and the one permission
> mistake that turns every request into a 502 with nothing in the log.

A dynamic site listens on a Unix socket in `/run/ratline/<slug>/app.sock` by
default, and nginx proxies to it. A socket is preferred over a TCP port because it
cannot be reached from another machine, needs no port allocation, and is subject to
filesystem permissions — a port on `127.0.0.1` is reachable by every other tenant
on the box.

`--listen port` allocates from the range in `ports:` for the cases that need it: a
framework that cannot bind a path, or a health checker that speaks only TCP.

## The 502 that leaves no trace

This is worth understanding once, because the symptom is so unhelpful.

`connect(2)` on a Unix socket requires **write** permission on the socket inode.
Not read — write. A socket created with the usual `0027` umask lands at `0640`:
owner read/write, group read only. nginx is in the tenant's group, opens the
socket, gets `EACCES`, and returns 502. The application is running perfectly. Its
log is empty, because no request ever reached it.

ratline avoids it in three places:

* The unit sets `UMask=0007` for socket sites, so the socket is created `0660`.
  Files the application creates are group-writable as a result, but the group is
  the tenant's own.
* Gunicorn is additionally started with `--umask 0117`, because it sets its own.
* `ExecStartPost` waits for the socket and chmods it, as a backstop for a
  framework that chmods the socket itself after binding.

`ratline doctor` recognises the state and names it, rather than reporting a generic
"socket does not accept connections":

    problem  socket  api.example.com
      the socket is mode 0640; nginx needs 0660 to connect, so every request is a 502

## Telling your application where to listen

There is no standard for this, so ratline sets three environment variables to the
same socket path and you read whichever your framework understands:

    PORT=/run/ratline/acme-app_example_com/app.sock
    RATLINE_SOCKET=/run/ratline/acme-app_example_com/app.sock
    SOCKET_PATH=/run/ratline/acme-app_example_com/app.sock

`PORT` is set to a path deliberately: Node's `server.listen()` accepts a path in
the same argument, so most applications need no change at all.

For a port site, `PORT` is the number and `HOST` is `127.0.0.1`.

See also: `ratline explain node`, `ratline explain diagnose`.
