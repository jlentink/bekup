package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"slices"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/jlentink/bekup/internal/runner/spec"
)

// Docker is the subset of the Docker Engine API the executor uses. The
// production implementation (NewMobyDocker) talks to the daemon over the
// unix socket of runner.toml docker.host; tests use a fake.
type Docker interface {
	Ping(ctx context.Context) error
	// ServerVersion returns the daemon's engine version ("27.3.1").
	ServerVersion(ctx context.Context) (string, error)
	// ImageInspect reports whether ref (repository@sha256:...) is local
	// and, if so, the image's declared VOLUME paths.
	ImageInspect(ctx context.Context, ref string) (ImageInfo, error)
	// ImagePull pulls ref and returns when the pull finished.
	ImagePull(ctx context.Context, ref string) error
	NetworkCreate(ctx context.Context, req NetworkRequest) (string, error)
	NetworkInspect(ctx context.Context, id string) (NetworkInfo, error)
	// NetworkList lists networks; labelFilter ("" = all) is a "key=value"
	// label filter.
	NetworkList(ctx context.Context, labelFilter string) ([]NetworkInfo, error)
	NetworkConnect(ctx context.Context, networkID, containerID string, ip netip.Addr) error
	// NetworkDisconnect force-disconnects containerID from networkID.
	NetworkDisconnect(ctx context.Context, networkID, containerID string) error
	NetworkRemove(ctx context.Context, id string) error
	ContainerCreate(ctx context.Context, name string, s spec.Spec) (string, error)
	// ContainerAttach attaches stdin, stdout and stderr (multiplexed).
	ContainerAttach(ctx context.Context, id string) (Attachment, error)
	ContainerStart(ctx context.Context, id string) error
	// ContainerWait waits until the container is not running and returns
	// its exit status.
	ContainerWait(ctx context.Context, id string) (int64, error)
	ContainerKill(ctx context.Context, id, signal string) error
	// ContainerRemove force-removes the container.
	ContainerRemove(ctx context.Context, id string) error
	// ContainerList lists all containers (also stopped ones) matching the
	// "key=value" label filter.
	ContainerList(ctx context.Context, labelFilter string) ([]ContainerInfo, error)
	ContainerInspect(ctx context.Context, id string) (ContainerInfo, error)
}

// Attachment is the hijacked attach connection: reads return the
// multiplexed stdout/stderr stream, writes go to the container's stdin.
type Attachment interface {
	io.Reader
	io.Writer
	// CloseWrite closes the container's stdin.
	CloseWrite() error
	Close() error
}

// ImageInfo describes a local image.
type ImageInfo struct {
	Present bool
	// Volumes are the image's VOLUME paths (Config.Volumes).
	Volumes []string
}

// NetworkRequest describes a network to create.
type NetworkRequest struct {
	Name     string
	Subnet   netip.Prefix
	Gateway  netip.Addr
	Internal bool
	Options  map[string]string
	Labels   map[string]string
}

// NetworkInfo describes an existing network.
type NetworkInfo struct {
	ID      string
	Name    string
	Labels  map[string]string
	Subnets []netip.Prefix
	// Gateways are the IPAM gateway addresses (the host's side of a
	// bridge network).
	Gateways []netip.Addr
	Created  time.Time
	// Containers are the IDs of attached containers (NetworkInspect only).
	Containers []string
}

// NetEndpoint is a container's address on one network.
type NetEndpoint struct {
	NetworkID string
	IP        netip.Addr
}

// ContainerInfo describes an existing container.
type ContainerInfo struct {
	ID      string
	Name    string
	Labels  map[string]string
	Created time.Time
	Running bool
	// Networks maps network names to the container's endpoints.
	Networks map[string]NetEndpoint
}

// IsNotFound reports whether err means the object does not exist.
func IsNotFound(err error) bool { return cerrdefs.IsNotFound(err) }

// mobyDocker implements Docker with the moby client.
type mobyDocker struct{ c *client.Client }

// NewMobyDocker connects to the daemon at host, which must be a unix://
// socket (runner.toml docker.host). The API version is negotiated.
func NewMobyDocker(host string) (Docker, io.Closer, error) {
	if !strings.HasPrefix(host, "unix:///") {
		return nil, nil, errors.New("executor: docker.host must be a unix:// socket")
	}
	c, err := client.New(client.WithHost(host))
	if err != nil {
		return nil, nil, fmt.Errorf("executor: docker client: %w", err)
	}
	return &mobyDocker{c: c}, c, nil
}

func (m *mobyDocker) Ping(ctx context.Context) error {
	_, err := m.c.Ping(ctx, client.PingOptions{})
	return err
}

func (m *mobyDocker) ServerVersion(ctx context.Context) (string, error) {
	v, err := m.c.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return "", err
	}
	return v.Version, nil
}

func (m *mobyDocker) ImageInspect(ctx context.Context, ref string) (ImageInfo, error) {
	res, err := m.c.ImageInspect(ctx, ref)
	if cerrdefs.IsNotFound(err) {
		return ImageInfo{}, nil
	}
	if err != nil {
		return ImageInfo{}, err
	}
	info := ImageInfo{Present: true}
	if res.Config != nil {
		for v := range res.Config.Volumes {
			info.Volumes = append(info.Volumes, v)
		}
		slices.Sort(info.Volumes)
	}
	return info, nil
}

func (m *mobyDocker) ImagePull(ctx context.Context, ref string) error {
	resp, err := m.c.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return err
	}
	return resp.Wait(ctx)
}

func (m *mobyDocker) NetworkCreate(ctx context.Context, req NetworkRequest) (string, error) {
	v4, v6 := true, false
	res, err := m.c.NetworkCreate(ctx, req.Name, client.NetworkCreateOptions{
		Driver:     "bridge",
		EnableIPv4: &v4,
		EnableIPv6: &v6,
		Internal:   req.Internal,
		IPAM: &network.IPAM{
			Driver: "default",
			Config: []network.IPAMConfig{{Subnet: req.Subnet, Gateway: req.Gateway}},
		},
		Options: req.Options,
		Labels:  req.Labels,
	})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

func networkInfo(n network.Network) NetworkInfo {
	info := NetworkInfo{ID: n.ID, Name: n.Name, Labels: n.Labels, Created: n.Created}
	for _, c := range n.IPAM.Config {
		if c.Subnet.IsValid() {
			info.Subnets = append(info.Subnets, c.Subnet.Masked())
		}
		if c.Gateway.IsValid() {
			info.Gateways = append(info.Gateways, c.Gateway.Unmap())
		}
	}
	return info
}

func (m *mobyDocker) NetworkInspect(ctx context.Context, id string) (NetworkInfo, error) {
	res, err := m.c.NetworkInspect(ctx, id, client.NetworkInspectOptions{})
	if err != nil {
		return NetworkInfo{}, err
	}
	info := networkInfo(res.Network.Network)
	for cid := range res.Network.Containers {
		info.Containers = append(info.Containers, cid)
	}
	return info, nil
}

func labelFilters(labelFilter string) client.Filters {
	f := client.Filters{}
	if labelFilter != "" {
		f = f.Add("label", labelFilter)
	}
	return f
}

func (m *mobyDocker) NetworkList(ctx context.Context, labelFilter string) ([]NetworkInfo, error) {
	res, err := m.c.NetworkList(ctx, client.NetworkListOptions{Filters: labelFilters(labelFilter)})
	if err != nil {
		return nil, err
	}
	out := make([]NetworkInfo, 0, len(res.Items))
	for i := range res.Items {
		out = append(out, networkInfo(res.Items[i].Network))
	}
	return out, nil
}

func (m *mobyDocker) NetworkConnect(ctx context.Context, networkID, containerID string, ip netip.Addr) error {
	_, err := m.c.NetworkConnect(ctx, networkID, client.NetworkConnectOptions{
		Container: containerID,
		EndpointConfig: &network.EndpointSettings{
			IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: ip},
		},
	})
	return err
}

func (m *mobyDocker) NetworkDisconnect(ctx context.Context, networkID, containerID string) error {
	_, err := m.c.NetworkDisconnect(ctx, networkID, client.NetworkDisconnectOptions{Container: containerID, Force: true})
	return err
}

func (m *mobyDocker) NetworkRemove(ctx context.Context, id string) error {
	_, err := m.c.NetworkRemove(ctx, id, client.NetworkRemoveOptions{})
	return err
}

func (m *mobyDocker) ContainerCreate(ctx context.Context, name string, s spec.Spec) (string, error) {
	res, err := m.c.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name, Config: s.Config, HostConfig: s.HostConfig, NetworkingConfig: s.Networking,
	})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

// hijacked adapts the attach connection to Attachment: reads go through
// the buffered reader that may already hold stream bytes.
type hijacked struct{ r client.ContainerAttachResult }

func (h *hijacked) Read(p []byte) (int, error)  { return h.r.Reader.Read(p) }
func (h *hijacked) Write(p []byte) (int, error) { return h.r.Conn.Write(p) }
func (h *hijacked) CloseWrite() error           { return h.r.CloseWrite() }
func (h *hijacked) Close() error                { return h.r.Conn.Close() }

func (m *mobyDocker) ContainerAttach(ctx context.Context, id string) (Attachment, error) {
	res, err := m.c.ContainerAttach(ctx, id, client.ContainerAttachOptions{Stream: true, Stdin: true, Stdout: true, Stderr: true})
	if err != nil {
		return nil, err
	}
	if res.Conn == nil || res.Reader == nil {
		if res.Conn != nil {
			discardErr(res.Conn.Close())
		}
		return nil, errors.New("executor: attach returned no connection")
	}
	return &hijacked{r: res}, nil
}

func (m *mobyDocker) ContainerStart(ctx context.Context, id string) error {
	_, err := m.c.ContainerStart(ctx, id, client.ContainerStartOptions{})
	return err
}

func (m *mobyDocker) ContainerWait(ctx context.Context, id string) (int64, error) {
	w := m.c.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case r := <-w.Result:
		if r.Error != nil && r.Error.Message != "" {
			return r.StatusCode, fmt.Errorf("executor: wait: %s", r.Error.Message)
		}
		return r.StatusCode, nil
	case err := <-w.Error:
		return -1, err
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}

func (m *mobyDocker) ContainerKill(ctx context.Context, id, signal string) error {
	_, err := m.c.ContainerKill(ctx, id, client.ContainerKillOptions{Signal: signal})
	return err
}

func (m *mobyDocker) ContainerRemove(ctx context.Context, id string) error {
	_, err := m.c.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	return err
}

func (m *mobyDocker) ContainerList(ctx context.Context, labelFilter string) ([]ContainerInfo, error) {
	res, err := m.c.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: labelFilters(labelFilter)})
	if err != nil {
		return nil, err
	}
	out := make([]ContainerInfo, 0, len(res.Items))
	for i := range res.Items {
		c := &res.Items[i]
		info := ContainerInfo{
			ID:      c.ID,
			Labels:  c.Labels,
			Created: time.Unix(c.Created, 0),
			Running: c.State == container.StateRunning,
		}
		if len(c.Names) > 0 {
			info.Name = strings.TrimPrefix(c.Names[0], "/")
		}
		if c.NetworkSettings != nil {
			info.Networks = endpoints(c.NetworkSettings.Networks)
		}
		out = append(out, info)
	}
	return out, nil
}

func (m *mobyDocker) ContainerInspect(ctx context.Context, id string) (ContainerInfo, error) {
	res, err := m.c.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return ContainerInfo{}, err
	}
	c := res.Container
	info := ContainerInfo{ID: c.ID, Name: strings.TrimPrefix(c.Name, "/")}
	if t, err := time.Parse(time.RFC3339Nano, c.Created); err == nil {
		info.Created = t
	}
	if c.State != nil {
		info.Running = c.State.Running
	}
	if c.Config != nil {
		info.Labels = c.Config.Labels
	}
	if c.NetworkSettings != nil {
		info.Networks = endpoints(c.NetworkSettings.Networks)
	}
	return info, nil
}

func endpoints(in map[string]*network.EndpointSettings) map[string]NetEndpoint {
	out := make(map[string]NetEndpoint, len(in))
	for name, ep := range in {
		if ep == nil {
			continue
		}
		out[name] = NetEndpoint{NetworkID: ep.NetworkID, IP: ep.IPAddress.Unmap()}
	}
	return out
}

// discardErr ignores errors of best-effort cleanup.
func discardErr(error) {}
