package runnerpb

const (
	// MaxChunkBytes is the maximum payload of one DataChunk (1 MiB).
	MaxChunkBytes = 1 << 20
	// MaxMessageBytes bounds every gRPC message in both directions: one
	// full chunk plus room for the envelope (requests carrying a target with
	// pins and keys stay far below it).
	MaxMessageBytes = MaxChunkBytes + 64<<10
	// APIVersion is reported by Health.
	APIVersion = "v1"
	// ServiceName is the fully qualified gRPC service name; the standard
	// health service on the runner's local socket reports it.
	ServiceName = "runner.v1.RunnerService"
)
