<?php
// DEV ONLY (docker-compose.dev.yml, D-114): Adminer may only log in to the
// bekup app DB. Without this plugin its login form accepts any host, which
// would turn the dev container into a generic proxy into every network it is
// attached to. TLS (verify-full, channel binding) comes from the libpq
// environment of the service (PGSSLMODE, PGSSLROOTCERT, PGCHANNELBINDING).
require_once 'plugins/login-servers.php';

return new AdminerLoginServers([
    'postgres' => ['server' => 'postgres', 'driver' => 'pgsql'],
]);
