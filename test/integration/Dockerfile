# The integration image: a real Ubuntu with systemd as PID 1, so site units are
# genuinely supervised rather than simulated.
#
# The panel's interface, built first so the Go build can embed the real thing.
#
# Without this stage the panel binary carries its placeholder page, and the suite
# would prove that the API works while saying nothing about whether the interface it
# is supposed to serve is even in the binary. The layout mirrors the repository
# because vite's outDir is relative to panel/web.
FROM node:22-slim AS web
WORKDIR /src
COPY panel/web/package.json panel/web/package-lock.json ./panel/web/
RUN cd panel/web && npm ci
COPY panel/web ./panel/web/
RUN cd panel/web && npm run build

# The builder must satisfy go.mod's own Go version. Pinned to 1.22 it failed with
# "go.mod requires go >= 1.25.0 (running go 1.22.12; GOTOOLCHAIN=local)" — the
# official images set GOTOOLCHAIN=local, so there is no silent toolchain download to
# paper over a stale pin. Bump this whenever go.mod's `go` line moves.
FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# After the source, so it lands on top of the placeholder a checkout carries.
COPY --from=web /src/internal/panel/web/dist ./internal/panel/web/dist
ENV CGO_ENABLED=0
RUN go build -trimpath -o /out/ratline ./cmd/ratline && \
    go build -trimpath -o /out/ratline-shell ./cmd/ratline-shell && \
    go build -trimpath -o /out/ratline-panel ./cmd/ratline-panel

FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive
ENV container=docker

# certbot is not optional here: without it the entire ACME section of the suite
# skips, and the Pebble and challtestsrv containers that compose starts alongside
# this one do nothing at all. The comment is above the instruction rather than inside
# it, because a # within a line continuation is handed to the shell and would comment
# out the rest of the command — installing fewer packages and still exiting 0.
#
# ufw and iproute2 are for the `db access` section: ratline verifies what mongod is
# bound to with ss, and refuses to open the port unless ufw stands in front of it.
# A minimal container has neither, where a real server has both.
RUN apt-get update && apt-get install -y --no-install-recommends \
        systemd systemd-sysv dbus \
        nginx openssh-server openssl ca-certificates curl \
        python3 python3-venv python3-pip \
        git rsync sudo procps xz-utils unzip jq sqlite3 \
        ufw iproute2 \
        certbot \
    && rm -rf /var/lib/apt/lists/*

# mongosh, for the database section. From MongoDB's own repository because Ubuntu does
# not package it, and pinned to the major version so a new release cannot change the
# suite's behaviour on an unrelated commit.
RUN set -eu; \
    apt-get update && apt-get install -y --no-install-recommends gnupg; \
    arch="$(dpkg --print-architecture)"; \
    curl -fsSL https://pgp.mongodb.com/server-8.0.asc \
      | gpg --dearmor -o /usr/share/keyrings/mongodb-server-8.0.gpg; \
    echo "deb [ arch=$arch signed-by=/usr/share/keyrings/mongodb-server-8.0.gpg ] \
https://repo.mongodb.org/apt/ubuntu noble/mongodb-org/8.0 multiverse" \
      > /etc/apt/sources.list.d/mongodb-org-8.0.list; \
    apt-get update && apt-get install -y --no-install-recommends \
        mongodb-mongosh mongodb-database-tools; \
    rm -rf /var/lib/apt/lists/*; \
    mongosh --version; \
    mongodump --version

# The units systemd would otherwise spend the container's life failing on.
RUN find /etc/systemd/system /lib/systemd/system \
        -path '*.wants/*' \
        \( -name '*getty*' -o -name '*udev*' -o -name '*console*' \) \
        -exec rm -f {} + || true

COPY --from=build /out/ratline /usr/local/bin/ratline
COPY --from=build /out/ratline-shell /usr/local/lib/ratline/ratline-shell
# The panel is installed but not set up: the suite runs `ratline-panel install`
# itself, against a server that by then already has tenants and sites — which is the
# situation every real install is in, and the one worth testing.
COPY --from=build /out/ratline-panel /usr/local/bin/ratline-panel
# ratline's own units are no longer copied in. They live in the binary's embedded
# templates and `ratline init` installs them, which is what lets a server set up from a
# single downloaded binary still renew its certificates — so the suite has to exercise
# that path rather than being handed the units by the image.
COPY test/integration/run.sh /usr/local/bin/ratline-integration
RUN chmod 0755 /usr/local/bin/ratline /usr/local/lib/ratline/ratline-shell \
        /usr/local/bin/ratline-panel /usr/local/bin/ratline-integration && \
    chown root:root /usr/local/bin/ratline /usr/local/lib/ratline/ratline-shell \
        /usr/local/bin/ratline-panel

# A unit that runs the suite once systemd is up, then shuts the container down
# with the suite's exit code. Running the tests from a shell before systemd is
# ready would test a machine no real deployment resembles.
RUN printf '%s\n' \
    '[Unit]' \
    'Description=ratline integration suite' \
    'After=multi-user.target nginx.service' \
    'Wants=nginx.service' \
    '[Service]' \
    'Type=oneshot' \
    'ExecStart=/usr/local/bin/ratline-integration' \
    'StandardOutput=journal+console' \
    'StandardError=journal+console' \
    '[Install]' \
    'WantedBy=multi-user.target' \
    > /etc/systemd/system/ratline-integration.service && \
    systemctl enable ratline-integration.service && \
    systemctl enable nginx.service

STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
