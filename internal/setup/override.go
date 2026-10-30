package setup

// composeShadowFiles are compose file names docker compose would read
// instead of (or in addition to) compose.yaml and compose.override.yaml:
// any of them in the deployment directory bypasses the checked files.
var composeShadowFiles = []string{
	"compose.yml", "docker-compose.yaml", "docker-compose.yml",
	"compose.override.yml", "docker-compose.override.yaml", "docker-compose.override.yml",
}

// composeEnvErrors are compose variables (in .env or the environment) that
// select other compose files or another project directory: each can bypass
// the pinned compose file. COMPOSE_PROJECT_NAME only renames the project
// (new, empty volumes), so it is a warning.
var composeEnvErrors = []string{"COMPOSE_FILE", "COMPOSE_PATH_SEPARATOR", "COMPOSE_PROJECT_DIRECTORY", "COMPOSE_ENV_FILES"}

const composeEnvWarning = "COMPOSE_PROJECT_NAME"

// dockerEnvErrors are Docker client variables docker compose also reads
// from .env: each sends the deployment to another daemon or context, or
// swaps the client configuration, where the checked files do not apply.
var dockerEnvErrors = []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "DOCKER_CONFIG"}
