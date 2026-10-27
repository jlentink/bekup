// Package executor is the runner's Docker executor (task 2.6, D-029,
// D-031, D-132). It is the only code that talks to the Docker daemon, over
// the unix socket of runner.toml docker.host.
//
// Run executes one dump/restore container:
//
//  1. pull the allowlisted image if missing (digest references only);
//  2. reserve a per-run subnet: a /28 from docker.run_subnet_pool, or a
//     /29 built around an IP-literal target (D-132), and publish it to the
//     target allowlist as one of the runner's own networks;
//  3. create the per-run network bekup-run-<id> (internal, IPv4 only,
//     inhibit_ipv4: no gateway address on the host, no masquerading);
//  4. connect the runner's own container to it with the fixed proxy
//     address;
//  5. start the per-run TCP proxy on that address and the target port. It
//     accepts only the dump container's fixed address and forwards each
//     connection to the one target, directly through the target allowlist
//     dialer (targets.Guard) or through the SSH tunnel whose bastion leg
//     uses the same dialer. TLS stays end to end;
//  6. create the hardened container from internal/runner/spec on that
//     network (fixed address, ExtraHosts target name -> proxy address),
//     attach stdin/stdout/stderr before start, write the stdin preamble
//     (zeroed right after), stream the restore payload, hand stdout to the
//     caller in bounded chunks with backpressure and stderr as bounded,
//     sanitized and redacted log lines;
//  7. wait for the exit code; a timeout sends SIGTERM and SIGKILL after
//     KillGrace, cancellation, stream loss or a failing restore input
//     SIGKILL at once;
//  8. always clean up in reverse order (container, proxy, network
//     membership, network, tunnel, subnet), also on errors and panics.
//
// The dump container is alone with the runner on its network: it has no
// default route, the gateway address is not on the host, Docker's embedded
// DNS does not forward queries from internal networks, other runs live on
// other subnets, the gRPC listener refuses peers from the pool and every
// active per-run subnet, and the proxy refuses every peer but the dump
// container. Reap removes leftovers by label at startup and every
// ReapInterval.
//
// RunSpec.Offline (task 2.7, D-134) runs a container without any network
// (NetworkMode none): steps 2 to 5 are skipped and the command may not
// need preamble files, so no credential reaches it (VerifyDump's
// pg_restore).
package executor
