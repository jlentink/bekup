package webdav

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/jlentink/bekup/internal/netguard"
	"github.com/jlentink/bekup/internal/storage"
)

// The overwrite probe (D-222). WebDAV has no exclusive create that every
// server implements, so "never overwrite" rests on MOVE with
// "Overwrite: F", which RFC 4918 requires servers to refuse (412) when the
// target exists. Some servers or proxies ignore the header; with them a
// MOVE would silently replace a backup. The probe proves the behavior on
// the real server before the first upload of every opened destination
// (and in Test): it writes two hidden temporary files A and B, moves A
// onto B with "Overwrite: F" and requires a 412 and B's content
// unchanged. With the Nextcloud flavor the chunked upload's final MOVE is
// probed the same way. A server that replaces B is never written to: every
// Put fails with storage.CodeOverwriteIgnored.

const (
	probeContentA = "bekup overwrite probe: this file must not replace the other one\n"
	probeContentB = "bekup overwrite probe: this content must survive\n"
)

// ProbeOverwrite runs the overwrite probe (once per destination: a later
// call returns the verdict). It returns nil when the server refuses to
// overwrite, an error of class storage.CodeOverwriteIgnored when it
// replaced the file, and other errors when the probe could not run.
func (d *Destination) ProbeOverwrite(ctx context.Context) error {
	if d.closed.Load() {
		return storage.ErrClosed
	}
	lay, err := d.layout(ctx)
	if err != nil {
		return err
	}
	return d.ensureProbe(ctx, lay)
}

// ensureProbe returns the cached verdict or runs the probe.
func (d *Destination) ensureProbe(ctx context.Context, lay *layout) error {
	switch d.probe.Load() {
	case probeRefused:
		return nil
	case probeIgnored:
		return errOverwriteIgnored()
	}
	release, err := acquire(ctx, d.probeSl)
	if err != nil {
		return err
	}
	defer release()
	switch d.probe.Load() {
	case probeRefused:
		return nil
	case probeIgnored:
		return errOverwriteIgnored()
	}
	if err := d.runProbe(ctx, lay); err != nil {
		if storage.Classify(err) == storage.CodeOverwriteIgnored {
			d.probe.Store(probeIgnored)
			d.s.logger.WarnContext(ctx, "webdav_overwrite_ignored", "host", d.s.host)
		}
		return err
	}
	d.probe.Store(probeRefused)
	return nil
}

func (d *Destination) runProbe(ctx context.Context, lay *layout) error {
	const op = "check that the server refuses overwrites"
	if err := d.mkcolAll(ctx, lay.files, lay.floor); err != nil {
		return err
	}
	na, err := newPartialName()
	if err != nil {
		return err
	}
	nb, err := newPartialName()
	if err != nil {
		return err
	}
	a, b := d.join(lay.files, []string{na}), d.join(lay.files, []string{nb})
	defer d.remove(ctx, a, false)
	defer d.remove(ctx, b, false)
	if err := d.putSmall(ctx, op, a, []byte(probeContentA)); err != nil {
		return err
	}
	if err := d.putSmall(ctx, op, b, []byte(probeContentB)); err != nil {
		return err
	}
	h := http.Header{"Destination": {d.urlOf(b, false)}, "Overwrite": {"F"}}
	code, _, err := d.do(ctx, op, request{method: "MOVE", segs: a, header: h}, nil)
	if err != nil {
		return err
	}
	if err := probeVerdict(op, code); err != nil {
		return err
	}
	if err := d.unchanged(ctx, op, b); err != nil {
		return err
	}
	if lay.uploads == nil || d.chunk.Load() == chunkOff {
		return nil
	}
	return d.probeChunked(ctx, op, lay, b)
}

// probeChunked probes the final MOVE of a chunked upload onto b.
func (d *Destination) probeChunked(ctx context.Context, op string, lay *layout, b []string) error {
	id, err := randomHex()
	if err != nil {
		return err
	}
	up := d.join(lay.uploads, []string{"bekup-" + id})
	dest := d.urlOf(b, false)
	switch err := d.startUpload(ctx, up, dest); {
	case errors.Is(err, errNoChunking):
		return nil // single PUTs: probed above
	case err != nil:
		return err
	}
	defer d.remove(ctx, up, true)
	if err := d.putChunk(ctx, up, dest, 1, []byte(probeContentA)); err != nil {
		return err
	}
	h := http.Header{
		"Destination": {dest}, "Overwrite": {"F"},
		"Oc-Total-Length": {strconv.Itoa(len(probeContentA))},
	}
	code, _, err := d.do(ctx, op, request{method: "MOVE", segs: d.join(up, []string{".file"}), header: h}, nil)
	if err != nil {
		return err
	}
	if err := probeVerdict(op, code); err != nil {
		return err
	}
	return d.unchanged(ctx, op, b)
}

// probeVerdict interprets the probe MOVE's status: 412 is the expected
// refusal, any 2xx means the server moved onto the existing file.
func probeVerdict(op string, code int) error {
	switch {
	case code == http.StatusPreconditionFailed:
		return nil
	case code >= 200 && code < 300:
		return errOverwriteIgnored()
	}
	return statusErr(op, code)
}

// unchanged checks that the probe target still holds probeContentB (a
// server that answers 412 but replaces the file anyway is caught too).
func (d *Destination) unchanged(ctx context.Context, op string, b []string) error {
	resp, err := d.send(ctx, op, request{method: http.MethodGet, segs: b, bounded: true, header: http.Header{"Accept-Encoding": {"identity"}}})
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		code := resp.StatusCode
		drain(resp)
		if code == http.StatusNotFound {
			return errOverwriteIgnored() // moved away or replaced
		}
		return statusErr(op, code)
	}
	got, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	drain(resp)
	if err != nil {
		return d.transportErr(ctx, op, err)
	}
	if !bytes.Equal(got, []byte(probeContentB)) {
		return errOverwriteIgnored()
	}
	return nil
}

// putSmall uploads a small body with a known length.
func (d *Destination) putSmall(ctx context.Context, op string, segs []string, data []byte) error {
	code, _, err := d.do(ctx, op, request{
		method: http.MethodPut, segs: segs,
		header: http.Header{"Content-Type": {"application/octet-stream"}},
	}, data)
	if err != nil {
		return err
	}
	switch code {
	case http.StatusCreated, http.StatusNoContent, http.StatusOK:
		return nil
	}
	return statusErr(op, code)
}

// ServerKind is what DetectServer recognized.
type ServerKind string

// Server kinds.
const (
	ServerUnknown   ServerKind = ""
	ServerNextcloud ServerKind = "nextcloud"
	ServerOwnCloud  ServerKind = "owncloud"
)

// DetectServer tells whether the server looks like Nextcloud or ownCloud:
// its status.php (at the URL's server root, or below the part before
// /remote.php; requested without credentials, since it may lie outside
// the configured folder) answers with the installation status, or the
// DAV header of the folder names Nextcloud. Only the kind is returned,
// never server text.
func (d *Destination) DetectServer(ctx context.Context) ServerKind {
	root := d.s.basePath
	if d.s.flavor == FlavorGeneric {
		root = nil
		for i, s := range d.s.basePath {
			if strings.EqualFold(s, "remote.php") {
				root = d.s.basePath[:i]
				break
			}
		}
	}
	if k := d.statusPHP(ctx, root); k != ServerUnknown {
		return k
	}
	lay, err := d.layout(ctx)
	if err != nil {
		return ServerUnknown
	}
	code, h, err := d.do(ctx, "read the server's capabilities", request{method: http.MethodOptions, segs: lay.files, coll: true}, nil)
	if err != nil || code >= 300 {
		return ServerUnknown
	}
	if strings.Contains(strings.ToLower(strings.Join(h.Values("Dav"), ",")), "nextcloud") {
		return ServerNextcloud
	}
	return ServerUnknown
}

func (d *Destination) statusPHP(ctx context.Context, root []string) ServerKind {
	resp, err := d.send(ctx, "read status.php", request{
		method: http.MethodGet, segs: d.join(root, []string{"status.php"}), noAuth: true, bounded: true,
	})
	if err != nil {
		return ServerUnknown
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return ServerUnknown
	}
	body, err := netguard.ReadAllLimited(resp.Body, maxStatusBody)
	if err != nil {
		return ServerUnknown
	}
	var st struct {
		Installed   *bool  `json:"installed"`
		ProductName string `json:"productname"`
		Version     string `json:"versionstring"`
	}
	if json.Unmarshal(body, &st) != nil || st.Installed == nil || st.Version == "" {
		return ServerUnknown
	}
	if strings.Contains(strings.ToLower(st.ProductName), "owncloud") {
		return ServerOwnCloud
	}
	return ServerNextcloud
}
