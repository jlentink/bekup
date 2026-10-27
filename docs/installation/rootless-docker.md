# Rootless Docker and userns-remap

> **Status:** guidance for the runner as implemented at the end of M2
> (tasks 2.4–2.7a: Docker executor, per-run networks, proxy and reaper).
> Neither mode is exercised by CI; verify the checks below on your host.
> Completed in task 7.5.

The runner is the only bekup component with Docker access (D-003), and
access to a rootful Docker socket is equivalent to root on the host. The
runner limits what it asks Docker to do (allowlisted, digest-pinned images,
a fixed hardened container spec, per-run `internal` networks; D-029–D-033),
but anyone who takes over the runner process holds the socket. Two optional
Docker features reduce the blast radius:

| | Rootless Docker | userns-remap |
|---|---|---|
| What runs unprivileged | the **daemon** and every container | every container (the daemon stays root) |
| Power of the Docker socket | the daemon user's rights, **not** host root | still host root |
| Runner container | normal (no extra settings) | must opt out with `userns_mode: host` |
| Escape from a dump container lands as | a subordinate UID of the daemon user | a subordinate UID (`dockremap`) |
| Recommended for | dedicated bekup hosts | hosts where the daemon must stay rootful |

Rootless Docker is the stronger option: it is the only one that takes host
root away from the socket.

## Rootless Docker

Install and start the daemon for a dedicated, unprivileged user as
described in the Docker documentation (`dockerd-rootless-setuptool.sh
install`, systemd user service, `loginctl enable-linger <user>`), then run
the **whole compose stack** on that daemon (`DOCKER_HOST` or a Docker
context pointing at it). bekup does not support splitting the stack across
two daemons: the runner's per-run networks and the `targets` network must
belong to the daemon that runs the runner.

**Socket path.** The rootless socket lives at
`$XDG_RUNTIME_DIR/docker.sock`, usually `/run/user/<uid>/docker.sock`.
Mount it at the path the runner expects (`[docker] host =
"unix:///var/run/docker.sock"`, only `unix://` sockets are accepted) with
a compose override; compose merges volumes by container path, so this
replaces the default source:

```yaml
# docker-compose.rootless.yml (use with -f docker-compose.yml -f docker-compose.rootless.yml)
services:
  runner:
    volumes:
      - /run/user/1000/docker.sock:/var/run/docker.sock:ro
```

**Group.** Inside the rootless daemon's user namespace the daemon user is
UID/GID 0, so the socket shows up in containers as owned by `0:0`. Check
the owner and mode as the runner sees them and set `DOCKER_GID`
accordingly (often `0`, which is the daemon user's own group on the host,
not host root):

```sh
ls -ln /run/user/1000/docker.sock                       # host view
docker run --rm -v /run/user/1000/docker.sock:/s:ro alpine stat -c '%u:%g %a' /s
```

The runner itself keeps `user: 65532:65532`; never run it as UID 0 to get
around a permission error, not even under rootless Docker. Give it only
the group that owns the socket. `[docker] gid` in `runner.toml` is
informational; the effective group is compose's `group_add`.

**Resource limits.** The runner sets memory, CPU and PID limits on every
dump container. Rootless Docker enforces them only on cgroup v2 with the
`cpu`, `memory` and `pids` controllers delegated to the user (systemd
`Delegate=` drop-in for `user@.service`). Check that `docker info` reports
`Cgroup Version: 2` and prints no "limit not supported" warnings; without
delegation the limits are silently ignored and a hostile or broken dump
can exhaust the host.

**Networking and the per-run networks.**

- The per-run networks are ordinary `internal` bridge networks inside
  RootlessKit's network namespace; creating them, attaching the runner and
  the dump container and the runner's per-run TCP proxy / SSH tunnel work
  unchanged.
- The runner reaches targets through RootlessKit's port driver (`pasta` or
  `slirp4netns`). Throughput is lower than with a rootful bridge, which
  matters for large dumps; `pasta` is the faster choice.
- Targets see connections from the host's address. Targets on the host's
  own loopback are unreachable by default (RootlessKit disables host
  loopback access). Keep it that way and use a routable address in
  `[targets]`.
- The proxy profiles publish ports 80/443. An unprivileged daemon cannot
  bind ports below 1024 unless you lower
  `net.ipv4.ip_unprivileged_port_start` or publish higher ports behind a
  host firewall redirect.
- `run_subnet_pool` and the fixed compose subnets live inside the
  namespace and can't clash with your LAN, but they must still not overlap
  `targets.allow_cidrs`.

**Self-detection.** The runner finds its own container through
`/proc/self/mountinfo`, then the container hostname. Rootless Docker keeps
container data under `~/.local/share/docker`, which detection handles. If
it still fails, set `[docker] self_container` (or
`BEKUP_DOCKER_SELF_CONTAINER`).

**Limitations.** Rootless Docker doesn't support every storage driver,
AppArmor profile or network option of a rootful daemon. The runner needs
only `internal` bridge networks with the options
`com.docker.network.bridge.inhibit_ipv4`, `enable_ip_masquerade=false` and
`enable_icc`, plus the default seccomp profile. A runner that fails to
create its per-run network refuses the run (fail closed) and logs the
Docker error.

## userns-remap

With `"userns-remap": "default"` in `/etc/docker/daemon.json`, every
container's UIDs map to a subordinate range (`/etc/subuid`, user
`dockremap`). An escape from a dump container then lands as an
unprivileged host UID. The daemon and its socket stay root.

**Runner.** Inside a remapped container the host's `docker` group isn't
mapped, so a remapped runner can't open the socket. Run only the runner
in the host user namespace with a compose override:

```yaml
services:
  runner:
    userns_mode: host
```

It still runs as UID 65532 with no capabilities, a read-only root and
`no-new-privileges`. `DOCKER_GID` is the host group of
`/var/run/docker.sock` (`stat -c %g /var/run/docker.sock`). The dump
containers the runner starts don't set a user-namespace mode, so they
**are** remapped.

**File ownership.** bekup refuses configuration and secret files that are
not owned by root or UID 65532 (D-041). In a remapped container a file
owned by host root looks like `nobody` (65534). For `serve`, `migrate` and
`certs-init`, either chown their bind-mounted files to the remapped IDs
(`<subuid start> + 65532`, for example `165532`, or `<subuid start>` for
root) or give those services `userns_mode: host` as well. The runner in
the host namespace keeps the normal rule. The app Postgres (UID 70) needs
the same treatment for its secret files.

**Networking.** userns-remap doesn't change networking: per-run networks,
the proxy and the target allowlist behave as on a plain rootful daemon.

**Limitation.** The socket is still root-equivalent. A compromised
runner could ask the daemon for a privileged or host-namespace container,
which the runner's own spec builder never does (D-031). userns-remap
protects against escapes from the dump containers, not against misuse of
the socket. Use rootless Docker for that.

## Checklist

- [ ] `docker info` shows `rootless` under Security Options (rootless) or
      `userns` (remap), `Cgroup Version: 2`, and no limit warnings.
- [ ] The runner is healthy (`docker compose ps`) and runs as 65532
      (`docker compose exec runner id` is not possible on distroless; use
      `docker inspect -f '{{.Config.User}}'`).
- [ ] A connection test (`testConnection`) succeeds, and afterwards no
      `bekup-run-*` networks or containers are left (`docker network ls`,
      `docker ps -a`).
- [ ] Memory, CPU and PID limits show up on a running dump container
      (`docker inspect -f '{{.HostConfig.Memory}} {{.HostConfig.PidsLimit}}'`).
