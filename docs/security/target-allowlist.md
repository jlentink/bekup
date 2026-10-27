# Runner target allowlist

The runner is the only bekup component that connects to your database
servers and SSH bastions (directly, or through the per-run proxy its dump
containers use). Which hosts it may reach is decided **only** by the
`[targets]` section of the runner's own `runner.toml`. Nothing `serve`
controls (connections in the database, the API, `bekup.toml`) can widen it
(D-019, D-029, D-130).

## Configuration

```toml
[targets]
# CIDRs the runner may connect to.
allow_cidrs = ["10.20.0.0/16", "2001:db8:20::/48"]
# Host names (exact match, case-insensitive, no wildcards) the runner may
# connect to, whatever address they resolve to (except always-denied ones).
allow_hosts = ["db.internal.example.com", "bastion.example.com"]
# Evaluated before the allow lists.
deny_cidrs = ["10.20.99.0/24"]
deny_hosts = ["legacy-db.internal.example.com"]
# Optional: what each SSH bastion may forward to ("<bastion>=<CIDR or name>").
bastion_targets = ["bastion.example.com=10.30.0.0/24", "bastion.example.com=db.internal"]
```

**An empty allowlist denies every target.** A fresh runner refuses all
connections and logs a startup warning ("allow_cidrs and allow_hosts are
empty ...") until you list your database networks or hosts. Requests for a
denied target fail with `PermissionDenied: target not allowed by the
runner's target allowlist`.

Rules for the values (checked at startup; the runner refuses to start on an
invalid list):

- `allow_cidrs` must not be `/0` and must not overlap an always-denied
  range (for example `0.0.0.0/1` or `::ffff:127.0.0.1/128` are rejected).
- IPv4-mapped IPv6 prefixes (`::ffff:10.0.0.0/104`) are treated as their
  IPv4 form.
- Host entries are names, not IP addresses: use `10.0.0.5/32` in
  `allow_cidrs` instead of `10.0.0.5` in `allow_hosts`. Configuration
  validation (`bekup config check`, runner start) rejects IP addresses and
  all-numeric names in `allow_hosts` and `deny_hosts`.

## How a connection is decided

For every address `A` of host name `H` (empty for an IP literal), in order:

1. **Always denied**, whatever the configuration: loopback (`127.0.0.0/8`,
   `::1`), link-local (`169.254.0.0/16`, `fe80::/10`) including the cloud
   metadata endpoints (`169.254.169.254`, `fd00:ec2::254`,
   `100.100.100.200`), unspecified, "this network" (`0.0.0.0/8`),
   multicast, broadcast, reserved ranges, Teredo, and NAT64/6to4 addresses
   that embed a denied IPv4 address. IPv4-mapped IPv6 addresses
   (`::ffff:127.0.0.1`) are unmapped first. This is the hard-denied set of
   the SSRF guard (D-120).
2. **The runner's own networks** are denied, so a target can never point
   back at the runner or another run: the control network that holds
   `listen.address`, every address of the runner's container, the whole
   `[docker] run_subnet_pool` (default `10.214.0.0/16`) and every active
   per-run network (D-132). Do not use the pool's range for databases.
3. **Docker networks** on the runner's daemon (D-138): every subnet of
   every Docker network and every network's gateway (the Docker host's side
   of the bridge) is denied unless `allow_cidrs` contains an entry that
   lies **inside** that Docker subnet and contains `A`; a gateway needs an
   entry of exactly its address (`172.18.0.1/32`). Broad entries such as
   `172.16.0.0/12` or `10.0.0.0/8` do not open Docker networks, and
   `allow_hosts` never does. To back up a database container on the same
   Docker host, list its network's subnet (`docker network inspect`) or the
   container's `/32` in `allow_cidrs`. The runner refreshes the list at
   startup, before every run and every minute.
4. `H` in `deny_hosts` or `A` in `deny_cidrs`: denied. A denied name is
   refused before any DNS query.
5. `A` in `allow_cidrs` or `H` in `allow_hosts`: allowed.
6. Anything else: denied.

The check runs twice:

- **When a request is validated**: the runner resolves the host, and
  **every** resolved address must be allowed (a name with one bad address
  is refused as a whole; at most 16 addresses).
- **When it dials**: the per-run proxy and the SSH tunnel connect through
  the allowlist dialer, which resolves again and re-checks the exact socket
  address right before the connection is made. A DNS answer that changes
  between validation and dial (DNS rebinding) cannot reach a denied
  address.

### SSH bastions

With an SSH tunnel the runner connects only to the bastion, so the bastion
must pass the full allowlist. The database host behind it is resolved by the
bastion; the runner checks it against `deny_hosts` and `deny_cidrs`, and an
IP literal must not be in an always-denied range other than loopback (a
database listening on the bastion's own `127.0.0.1` is allowed; the
bastion's metadata endpoint is not).

A bastion can reach networks the runner cannot, so restrict what it may
forward to with `bastion_targets` (D-138). Each entry is
`<bastion>=<target>`: the bastion as a host name or IP address, the target
as a CIDR (`10.30.0.0/24`, `127.0.0.1/32` for a database on the bastion
itself) or an exact host name the bastion resolves (`db.internal`). A
bastion listed at least once (matched by its name or any of its resolved
addresses) may only forward to its listed targets: an IP-literal target
must be in one of its CIDRs, a host-name target must be listed by name (a
CIDR cannot match a name the runner never resolves). When several entries
match one bastion, every one of them must allow the target.

**Bastions that are not listed keep the old behavior**: they may forward to
any host they reach behind the deny lists. The runner logs a startup
warning while `bastion_targets` is empty, and a `runner_bastion_unrestricted`
warning the first time each unlisted bastion is used.

### IP-literal targets and TLS verification

The dump container reaches its target only through the runner's per-run
proxy. For a host name the runner maps the name to the proxy
(`ExtraHosts`), so the client still verifies the certificate against that
name. An IP literal has no name to map (D-132):

- With TLS `unverified` or `disabled`, the client simply dials the proxy
  address.
- With `pinned` TLS (`verify-full`) **through an SSH tunnel**, the runner
  builds the per-run network as a `/29` around the literal and holds that
  exact address itself, so the client verifies the certificate's IP SAN.
  This fails closed when the address is not IPv4, is the network or
  broadcast address of its `/29`, or that `/29` overlaps the runner's or
  Docker's networks, a Docker network gateway, the runner's DNS servers or
  default gateways, or the bastion; only one run at a time can use a given
  `/29`.
- With `pinned` TLS and **no tunnel**, the run is refused: the runner
  cannot hold an address it must also connect to. Use a DNS name that the
  certificate covers (for example through your DNS or the runner's
  `extra_hosts`) or an SSH tunnel.

## What this protects against

If `serve` (or an admin account) is compromised, the attacker can only ask
the runner for targets inside the allowlist, with the credentials bekup
already holds. They cannot use the runner as a port scanner or SSRF proxy
into the rest of your network, the Docker host, the runner itself or a cloud
metadata service. Keep the allowlist as narrow as your database servers
allow: list individual hosts or small subnets rather than whole private
ranges.

Related: [hardening](hardening.md), [mTLS certificates](mtls-certs.md),
`docs/DECISIONS.md` D-029, D-098, D-120, D-130, D-132, D-138.
