package executor

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"

	"github.com/jlentink/bekup/internal/runner/spec"
)

const (
	selfID   = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"
	selfIP   = "172.30.0.5"
	ctlNetID = "ctlnet0000"
)

// fakeDocker is an in-memory Docker with simulated container processes.
type fakeDocker struct {
	mu         sync.Mutex
	calls      []string
	fail       map[string]error
	panicOn    string
	images     map[string]bool
	volumes    map[string][]string // image VOLUME paths by ref
	version    string              // ServerVersion
	nets       map[string]*NetworkInfo
	conts      map[string]*fakeContainer
	foreign    []NetworkInfo
	inspect    map[string]ContainerInfo // extra containers for ContainerInspect
	selfImage  string                   // Config.Image of the runner's own container
	proc       func(p *fakeProc)
	ignoreTerm bool
	seq        int
	lastNetReq NetworkRequest
	lastSpec   spec.Spec
	lastCont   *fakeContainer
	onList     func() // called at the start of ContainerList (reaper)
}

func newFakeDocker() *fakeDocker {
	return &fakeDocker{
		fail:    map[string]error{},
		images:  map[string]bool{},
		volumes: map[string][]string{},
		version: "27.3.1",
		nets:    map[string]*NetworkInfo{},
		conts:   map[string]*fakeContainer{},
		inspect: map[string]ContainerInfo{},
		proc:    func(p *fakeProc) { p.readPreamble(); p.exit(0) },
	}
}

func (f *fakeDocker) record(op string) error {
	f.mu.Lock()
	f.calls = append(f.calls, op)
	err := f.fail[op]
	p := f.panicOn
	f.mu.Unlock()
	if p == op {
		panic("fake docker panic in " + op)
	}
	return err
}

func (f *fakeDocker) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeDocker) nextID(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s%04d", prefix, f.seq)
}

func (f *fakeDocker) Ping(context.Context) error { return f.record("Ping") }

func (f *fakeDocker) ServerVersion(context.Context) (string, error) {
	if err := f.record("ServerVersion"); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.version, nil
}

func (f *fakeDocker) ImageInspect(_ context.Context, ref string) (ImageInfo, error) {
	if err := f.record("ImageInspect"); err != nil {
		return ImageInfo{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.images[ref] {
		return ImageInfo{}, nil
	}
	return ImageInfo{Present: true, Volumes: slices.Clone(f.volumes[ref])}, nil
}

func (f *fakeDocker) ImagePull(_ context.Context, ref string) error {
	if err := f.record("ImagePull"); err != nil {
		return err
	}
	f.mu.Lock()
	f.images[ref] = true
	f.mu.Unlock()
	return nil
}

func (f *fakeDocker) NetworkCreate(_ context.Context, req NetworkRequest) (string, error) {
	if err := f.record("NetworkCreate"); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !req.Internal || req.Options["com.docker.network.bridge.inhibit_ipv4"] != "true" {
		return "", errors.New("fake: insecure network request")
	}
	for _, n := range f.nets {
		if n.Subnets[0].Overlaps(req.Subnet) {
			return "", errors.New("fake: pool overlaps")
		}
	}
	id := f.nextID("net")
	f.nets[id] = &NetworkInfo{ID: id, Name: req.Name, Labels: req.Labels, Subnets: []netip.Prefix{req.Subnet}, Created: time.Now()}
	f.lastNetReq = req
	return id, nil
}

func (f *fakeDocker) NetworkInspect(_ context.Context, id string) (NetworkInfo, error) {
	if err := f.record("NetworkInspect"); err != nil {
		return NetworkInfo{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if id == ctlNetID {
		return NetworkInfo{ID: ctlNetID, Name: "ctl", Subnets: []netip.Prefix{netip.MustParsePrefix("172.30.0.0/24")}}, nil
	}
	n, ok := f.nets[id]
	if !ok {
		return NetworkInfo{}, cerrdefs.ErrNotFound
	}
	return *n, nil
}

func (f *fakeDocker) NetworkList(_ context.Context, label string) ([]NetworkInfo, error) {
	if err := f.record("NetworkList"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []NetworkInfo
	if label == "" {
		out = append(out, f.foreign...)
	}
	for _, n := range f.nets {
		k, v, _ := strings.Cut(label, "=")
		if label == "" || n.Labels[k] == v {
			out = append(out, *n)
		}
	}
	return out, nil
}

func (f *fakeDocker) NetworkConnect(_ context.Context, netID, cid string, _ netip.Addr) error {
	if err := f.record("NetworkConnect"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.nets[netID]
	if !ok {
		return cerrdefs.ErrNotFound
	}
	n.Containers = append(n.Containers, cid)
	return nil
}

func (f *fakeDocker) NetworkDisconnect(_ context.Context, netID, cid string) error {
	if err := f.record("NetworkDisconnect"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.nets[netID]
	if !ok {
		return cerrdefs.ErrNotFound
	}
	n.Containers = slices.DeleteFunc(n.Containers, func(c string) bool { return c == cid })
	return nil
}

func (f *fakeDocker) NetworkRemove(_ context.Context, id string) error {
	if err := f.record("NetworkRemove"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.nets[id]
	if !ok {
		return cerrdefs.ErrNotFound
	}
	if len(n.Containers) > 0 {
		return errors.New("fake: network has active endpoints")
	}
	delete(f.nets, id)
	return nil
}

func (f *fakeDocker) ContainerCreate(_ context.Context, name string, s spec.Spec) (string, error) {
	if err := f.record("ContainerCreate"); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID("ctr")
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &fakeContainer{
		id: id, name: name, spec: s, created: time.Now(),
		inR: inR, inW: inW, outR: outR, outW: outW,
		exited: make(chan struct{}), killed: make(chan struct{}),
	}
	f.conts[id] = c
	f.lastSpec = s
	f.lastCont = c
	return id, nil
}

func (f *fakeDocker) container(id string) *fakeContainer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conts[id]
}

func (f *fakeDocker) ContainerAttach(_ context.Context, id string) (Attachment, error) {
	if err := f.record("ContainerAttach"); err != nil {
		return nil, err
	}
	c := f.container(id)
	if c == nil {
		return nil, cerrdefs.ErrNotFound
	}
	return &fakeAttach{c: c}, nil
}

func (f *fakeDocker) ContainerStart(_ context.Context, id string) error {
	if err := f.record("ContainerStart"); err != nil {
		return err
	}
	c := f.container(id)
	if c == nil {
		return cerrdefs.ErrNotFound
	}
	go f.proc(&fakeProc{c: c})
	return nil
}

func (f *fakeDocker) ContainerWait(ctx context.Context, id string) (int64, error) {
	if err := f.record("ContainerWait"); err != nil {
		return -1, err
	}
	c := f.container(id)
	if c == nil {
		return -1, cerrdefs.ErrNotFound
	}
	select {
	case <-c.exited:
		return c.code, nil
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}

func (f *fakeDocker) ContainerKill(_ context.Context, id, signal string) error {
	if err := f.record("ContainerKill:" + signal); err != nil {
		return err
	}
	c := f.container(id)
	if c == nil {
		return cerrdefs.ErrNotFound
	}
	if signal == "SIGTERM" && f.ignoreTerm {
		return nil
	}
	c.kill(signal)
	return nil
}

func (f *fakeDocker) ContainerRemove(_ context.Context, id string) error {
	if err := f.record("ContainerRemove"); err != nil {
		return err
	}
	f.mu.Lock()
	c, ok := f.conts[id]
	delete(f.conts, id)
	f.mu.Unlock()
	if !ok {
		return cerrdefs.ErrNotFound
	}
	c.kill("SIGKILL")
	return nil
}

func (f *fakeDocker) ContainerList(_ context.Context, label string) ([]ContainerInfo, error) {
	if err := f.record("ContainerList"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	hook := f.onList
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []ContainerInfo
	k, v, _ := strings.Cut(label, "=")
	for _, c := range f.conts {
		if c.spec.Config.Labels[k] == v {
			out = append(out, ContainerInfo{ID: c.id, Name: c.name, Labels: c.spec.Config.Labels, Created: c.created})
		}
	}
	return out, nil
}

func (f *fakeDocker) ContainerInspect(_ context.Context, id string) (ContainerInfo, error) {
	if err := f.record("ContainerInspect"); err != nil {
		return ContainerInfo{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if id == selfID || id == "runnerhost" || id == selfID[:12] {
		return ContainerInfo{ID: selfID, Name: "runner", Running: true, Image: f.selfImage, Networks: map[string]NetEndpoint{
			"ctl": {NetworkID: ctlNetID, IP: netip.MustParseAddr(selfIP)},
		}}, nil
	}
	if c, ok := f.inspect[id]; ok {
		return c, nil
	}
	return ContainerInfo{}, cerrdefs.ErrNotFound
}

type fakeContainer struct {
	id, name   string
	spec       spec.Spec
	created    time.Time
	inR        *io.PipeReader
	inW        *io.PipeWriter
	outR       *io.PipeReader
	outW       *io.PipeWriter
	once       sync.Once
	code       int64
	exited     chan struct{}
	killOnce   sync.Once
	killed     chan struct{}
	mu         sync.Mutex
	closeWrite bool
	stdin      bytes.Buffer
}

func (c *fakeContainer) finish(code int64) {
	c.once.Do(func() {
		c.code = code
		discardErr(c.outW.Close())
		discardErr(c.inR.CloseWithError(io.ErrClosedPipe))
		close(c.exited)
	})
}

func (c *fakeContainer) kill(signal string) {
	c.killOnce.Do(func() { close(c.killed) })
	if signal == "SIGKILL" {
		c.finish(137)
	} else {
		c.finish(143)
	}
}

type fakeAttach struct{ c *fakeContainer }

func (a *fakeAttach) Read(p []byte) (int, error)  { return a.c.outR.Read(p) }
func (a *fakeAttach) Write(p []byte) (int, error) { return a.c.inW.Write(p) }
func (a *fakeAttach) CloseWrite() error {
	a.c.mu.Lock()
	a.c.closeWrite = true
	a.c.mu.Unlock()
	return a.c.inW.Close()
}

func (a *fakeAttach) Close() error {
	discardErr(a.c.inW.CloseWithError(io.ErrClosedPipe))
	return a.c.outR.Close()
}

// fakeProc is the simulated process inside a container.
type fakeProc struct{ c *fakeContainer }

// readPreamble reads stdin up to and including "END\n" and records it.
func (p *fakeProc) readPreamble() string {
	var b []byte
	one := make([]byte, 1)
	for !bytes.HasSuffix(b, []byte("\nEND\n")) {
		n, err := p.c.inR.Read(one)
		if err != nil {
			return string(b)
		}
		b = append(b, one[:n]...)
	}
	p.c.mu.Lock()
	p.c.stdin.Write(b)
	p.c.mu.Unlock()
	return string(b)
}

// readAll reads the rest of stdin.
func (p *fakeProc) readAll() ([]byte, error) { return io.ReadAll(p.c.inR) }

func (p *fakeProc) frame(stream byte, b []byte) error {
	buf := make([]byte, 0, 8+len(b))
	buf = append(buf, stream, 0, 0, 0)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(b))) //nolint:gosec // test frames are small
	buf = append(buf, b...)
	if _, err := p.c.outW.Write(buf); err != nil {
		return err
	}
	return nil
}

func (p *fakeProc) stdout(b []byte) error { return p.frame(1, b) }
func (p *fakeProc) stderr(s string) error { return p.frame(2, []byte(s)) }
func (p *fakeProc) exit(code int64)       { p.c.finish(code) }
func (p *fakeProc) killed() <-chan struct{} {
	return p.c.killed
}
