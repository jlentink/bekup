## bekup

bekup: secure, self-hosted database backup manager

### Synopsis

bekup schedules and runs PostgreSQL, MySQL and MariaDB backups with native dump tools,
encrypts them and writes them to disk, S3 and SFTP.

### Options

```
      --config string      config file path (or BEKUP_CONFIG); a missing explicit file is an error
      --dev                dev mode (or BEKUP_DEV=1): relaxed guardrails, never use in production
  -h, --help               help for bekup
      --log-level string   log level: debug, info, warn or error (overrides log.level)
```

### SEE ALSO

* [bekup admin](bekup_admin.md)	 - Break-glass account administration: create admins, reset passwords and 2FA, unlock, disable
* [bekup audit](bekup_audit.md)	 - Verify the audit log hash chain and sign its head
* [bekup certs](bekup_certs.md)	 - Create, rotate and inspect the private CA and component mTLS certificates
* [bekup completion](bekup_completion.md)	 - Generate the autocompletion script for the specified shell
* [bekup config](bekup_config.md)	 - Validate or print the effective configuration
* [bekup decrypt](bekup_decrypt.md)	 - Decrypt a backup artifact offline (not implemented yet)
* [bekup healthcheck](bekup_healthcheck.md)	 - Probe a local bekup component (container healthchecks; exit 0 healthy, 1 unhealthy)
* [bekup keys](bekup_keys.md)	 - Generate and rotate the master keyring
* [bekup migrate](bekup_migrate.md)	 - Apply database migrations (as bekup_owner); default action: up
* [bekup runner](bekup_runner.md)	 - Run the Docker runner (mTLS gRPC, reads runner.toml)
* [bekup serve](bekup_serve.md)	 - Run the web UI, GraphQL API and scheduler
* [bekup verify](bekup_verify.md)	 - Verify a backup artifact offline (not implemented yet)
* [bekup version](bekup_version.md)	 - Print the bekup version
* [bekup worker](bekup_worker.md)	 - Run background workers only (not implemented yet)

