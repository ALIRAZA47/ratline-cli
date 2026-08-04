# The integration image: a real Ubuntu with systemd as PID 1, so site units are
# genuinely supervised rather than simulated.
FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
RUN go build -trimpath -o /out/ratline ./cmd/ratline && \
    go build -trimpath -o /out/ratline-shell ./cmd/ratline-shell

FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive
ENV container=docker

RUN apt-get update && apt-get install -y --no-install-recommends \
        systemd systemd-sysv dbus \
        nginx openssh-server openssl ca-certificates curl \
        python3 python3-venv python3-pip \
        git rsync sudo procps xz-utils jq sqlite3 \
    && rm -rf /var/lib/apt/lists/*

# The units systemd would otherwise spend the container's life failing on.
RUN find /etc/systemd/system /lib/systemd/system \
        -path '*.wants/*' \
        \( -name '*getty*' -o -name '*udev*' -o -name '*console*' \) \
        -exec rm -f {} + || true

COPY --from=build /out/ratline /usr/local/bin/ratline
COPY --from=build /out/ratline-shell /usr/local/lib/ratline/ratline-shell
COPY packaging/systemd/ /etc/systemd/system/
COPY test/integration/run.sh /usr/local/bin/ratline-integration
RUN chmod 0755 /usr/local/bin/ratline /usr/local/lib/ratline/ratline-shell \
        /usr/local/bin/ratline-integration && \
    chown root:root /usr/local/bin/ratline /usr/local/lib/ratline/ratline-shell

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
