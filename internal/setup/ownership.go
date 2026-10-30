package setup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// OwnershipMode decides how setup hands files to the container users.
type OwnershipMode string

// Ownership modes.
const (
	// OwnershipAuto picks none on Docker Desktop file systems, chown as
	// root, print otherwise.
	OwnershipAuto OwnershipMode = "auto"
	// OwnershipChown chmods and Lchowns every entry (needs CAP_CHOWN).
	OwnershipChown OwnershipMode = "chown"
	// OwnershipPrint chmods and prints the chown commands for the operator.
	OwnershipPrint OwnershipMode = "print"
	// OwnershipNone only chmods (Docker Desktop maps ownership itself).
	OwnershipNone OwnershipMode = "none"
)

// ParseOwnershipMode validates a --ownership value.
func ParseOwnershipMode(s string) (OwnershipMode, error) {
	switch m := OwnershipMode(s); m {
	case OwnershipAuto, OwnershipChown, OwnershipPrint, OwnershipNone:
		return m, nil
	}
	return "", fmt.Errorf("--ownership must be auto, chown, print or none (got %q)", bounded(s))
}

// desktopFSTypes are the file systems Docker Desktop (and similar VMs) use
// for host bind mounts; they map ownership themselves.
var desktopFSTypes = map[string]bool{
	"fakeowner":     true,
	"virtiofs":      true,
	"fuse.grpcfuse": true,
	"osxfs":         true,
	"9p":            true,
}

// Mount is one line of /proc/self/mountinfo.
type Mount struct {
	Point  string
	FSType string
}

// maxMountinfoLines bounds the parser.
const maxMountinfoLines = 100000

// ParseMountinfo parses /proc/self/mountinfo (proc(5)): field 5 is the
// mount point (octal-escaped), the file system type follows the " - "
// separator.
func ParseMountinfo(r io.Reader) ([]Mount, error) {
	var out []Mount
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		if n > maxMountinfoLines {
			return nil, errors.New("mountinfo: too many lines")
		}
		line := sc.Text()
		sep := strings.Index(line, " - ")
		if sep < 0 {
			return nil, fmt.Errorf("mountinfo line %d: no separator", n)
		}
		pre := strings.Fields(line[:sep])
		post := strings.Fields(line[sep+3:])
		if len(pre) < 5 || len(post) < 1 {
			return nil, fmt.Errorf("mountinfo line %d: too few fields", n)
		}
		out = append(out, Mount{Point: unescapeMount(pre[4]), FSType: post[0]})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("mountinfo: %w", err)
	}
	return out, nil
}

// unescapeMount decodes the \ooo escapes of mountinfo paths.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// mountOf returns the mount that contains abs (the longest mount point
// that is abs or one of its parents), and whether abs is that mount point.
func mountOf(mounts []Mount, abs string) (Mount, bool, bool) {
	var best Mount
	found := false
	for _, m := range mounts {
		if within(abs, m.Point) && (!found || len(m.Point) >= len(best.Point)) {
			best, found = m, true
		}
	}
	return best, found, found && filepath.Clean(best.Point) == filepath.Clean(abs)
}

func within(p, dir string) bool {
	p, dir = filepath.Clean(p), filepath.Clean(dir)
	if dir == "/" || p == dir {
		return true
	}
	return strings.HasPrefix(p, dir+string(filepath.Separator))
}

// sysEnv is what ownership detection and application need from the
// system; tests replace it. Paths are relative to the os.Root.
type sysEnv struct {
	mountinfo func() ([]Mount, error)
	euid      func() int
	// owner returns the numeric owner of an entry (fi comes from Lstat).
	owner func(r *os.Root, rel string, fi fs.FileInfo) (uid, gid int, ok bool)
	// lchown and chmod change an entry (dir: a directory entry) without
	// following symlinks, on a descriptor (entryChown, entryChmod).
	lchown func(r *os.Root, rel string, dir bool, uid, gid int) error
	chmod  func(r *os.Root, rel string, dir bool, mode fs.FileMode) error
}

func defaultSysEnv() sysEnv {
	return sysEnv{
		mountinfo: func() (_ []Mount, retErr error) {
			f, err := os.Open("/proc/self/mountinfo")
			if err != nil {
				return nil, err
			}
			defer func() { retErr = errors.Join(retErr, f.Close()) }()
			return ParseMountinfo(f)
		},
		euid:   os.Geteuid,
		owner:  func(_ *os.Root, _ string, fi fs.FileInfo) (int, int, bool) { return fileOwner(fi) },
		lchown: entryChown,
		chmod:  entryChmod,
	}
}

// resolveOwnership turns auto into a concrete mode and explains the choice.
func resolveOwnership(mode OwnershipMode, root string, env sysEnv) (OwnershipMode, string) {
	if mode != OwnershipAuto {
		return mode, "chosen with --ownership"
	}
	if mounts, err := env.mountinfo(); err == nil {
		if m, ok, _ := mountOf(mounts, root); ok && desktopFSTypes[m.FSType] {
			return OwnershipNone, fmt.Sprintf("auto: %s is on a %s mount (Docker Desktop maps ownership itself)", root, m.FSType)
		}
	}
	if env.euid() == 0 {
		return OwnershipChown, "auto: running as root"
	}
	return OwnershipPrint, "auto: not running as root"
}

// modeBits are the mode bits the manifest decides (setuid, setgid and
// sticky are never wanted).
const modeBits = fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky

// wantMode is the mode an entry must have given its current mode: the
// manifest mode, or for an upper bound (ModeIsMax) the current permission
// bits without the ones the manifest does not allow.
func wantMode(e Entry, cur fs.FileMode) fs.FileMode {
	if e.ModeIsMax {
		return cur.Perm() & e.Mode
	}
	return e.Mode
}

// applyOwnership applies the manifest to every existing entry through the
// os.Root r (opened on the deployment directory). Per entry: the mode is
// changed only when it differs (chmod of a file that belongs to another
// user needs CAP_FOWNER, which the setup container does not have, so in
// chown mode such a file is first Lchown'ed to the current user, then
// chmod'ed, then Lchown'ed to its owner: CAP_CHOWN suffices), then the
// owner is set (chown mode) when it differs. Missing optional entries are
// skipped; symlinks are refused. In print mode the chown commands are
// written to w. Offline entries on a separate mount (the CA media) that
// does not support Unix modes or owners (FAT/exFAT: EPERM, ENOTSUP) are
// left as they are with a warning (the CA key is age-encrypted); the
// returned warnings are for the operator.
func applyOwnership(r *os.Root, entries []Entry, mode OwnershipMode, env sysEnv, w io.Writer) ([]string, error) {
	type pending struct {
		e  Entry
		fi fs.FileInfo
	}
	var todo []pending
	for _, e := range entries {
		fi, err := lstatIn(r, e.Path)
		if err != nil {
			return nil, err
		}
		if fi == nil {
			if e.Optional {
				continue
			}
			return nil, fmt.Errorf("%s is missing", e.Path)
		}
		if (e.Kind == KindDir) != fi.IsDir() || (e.Kind == KindFile && !fi.Mode().IsRegular()) {
			return nil, fmt.Errorf("%s is not a %s", e.Path, e.Kind)
		}
		todo = append(todo, pending{e, fi})
	}
	var warnings []string
	offlineMounts := map[string]bool{}
	tolerated := func(e Entry, op string, err error) bool {
		if !e.Offline || !isPermOrUnsupported(err) {
			return false
		}
		dir := e.Path
		if e.Kind == KindFile {
			dir = path.Dir(e.Path)
		}
		onMedia, ok := offlineMounts[dir]
		if !ok {
			onMedia = onMount(env, filepath.Join(r.Name(), filepath.FromSlash(dir)))
			offlineMounts[dir] = onMedia
		}
		if !onMedia {
			return false
		}
		warnings = append(warnings, fmt.Sprintf("%s: %s is not supported by the offline media mounted at %s (FAT/exFAT?): left as is. The CA key is age-encrypted, but a POSIX file system (e.g. ext4 on LUKS) is recommended for the CA media (docs/security/mtls-certs.md)", e.Path, op, dir))
		return true
	}
	euid := env.euid()
	for _, p := range todo {
		e, rel := p.e, p.e.Path
		uid, gid, known := env.owner(r, rel, p.fi)
		cur := p.fi.Mode() & modeBits
		if want := wantMode(e, cur); cur != want {
			if mode == OwnershipChown && known && uid != euid {
				// Take the entry over first: chmod is reserved to the owner
				// (or CAP_FOWNER); the owner is set again below.
				if err := env.lchown(r, rel, e.Kind == KindDir, euid, -1); err != nil {
					if tolerated(e, "chown", err) {
						continue
					}
					return warnings, fmt.Errorf("chown %s to %d (to change its mode): %w (use --ownership print to get the commands instead)", rel, euid, err)
				}
				uid = euid
			}
			if err := env.chmod(r, rel, e.Kind == KindDir, want); err != nil {
				if tolerated(e, "chmod", err) {
					continue
				}
				hint := ""
				if known && uid != euid && mode != OwnershipChown {
					hint = fmt.Sprintf(" (it belongs to UID %d: run setup as root with CAP_CHOWN and --ownership chown, or fix the mode on the host)", uid)
				}
				return warnings, fmt.Errorf("chmod %s to %04o: %w%s", rel, want, err, hint)
			}
		}
		if mode == OwnershipChown && (!known || uid != e.UID || gid != e.GID) {
			if err := env.lchown(r, rel, e.Kind == KindDir, e.UID, e.GID); err != nil {
				if tolerated(e, "chown", err) {
					continue
				}
				return warnings, fmt.Errorf("chown %s to %d:%d: %w (use --ownership print to get the commands instead)", rel, e.UID, e.GID, err)
			}
		}
	}
	if mode == OwnershipPrint {
		entries := make([]Entry, 0, len(todo))
		for _, p := range todo {
			entries = append(entries, p.e)
		}
		return warnings, printChown(w, entries)
	}
	return warnings, nil
}

// printChown writes the chown commands, grouped by owner, for the operator
// to run as root in the deployment directory on the host.
func printChown(w io.Writer, entries []Entry) error {
	groups := map[string][]string{}
	for _, e := range entries {
		k := fmt.Sprintf("%d:%d", e.UID, e.GID)
		groups[k] = append(groups[k], e.Path)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("Run as root in the deployment directory on the host:\n")
	const perLine = 6
	for _, k := range keys {
		paths := groups[k]
		for i := 0; i < len(paths); i += perLine {
			fmt.Fprintf(&b, "  chown -h %s -- %s\n", k, strings.Join(paths[i:min(i+perLine, len(paths))], " "))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// checkEntryFD verifies the type of an opened entry on its descriptor.
func checkEntryFD(f *os.File, rel string, dir bool) error {
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	switch {
	case dir && !fi.IsDir():
		return fmt.Errorf("%s is not a directory", rel)
	case !dir && !fi.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file (a symlink or special file appeared)", rel)
	}
	if n, ok := linkCount(fi); ok && !dir && n != 1 {
		return fmt.Errorf("%s has %d hard links: refusing to change it (a hard link could be another file of the host; replace it with a copy)", rel, n)
	}
	return nil
}

func closeWith(f *os.File, err error) error { return errors.Join(err, f.Close()) }

// entryChown and entryChmod change an entry through a descriptor opened
// with openEntry (no symlink is ever followed, the type is re-checked on
// the descriptor).
func entryChown(r *os.Root, rel string, dir bool, uid, gid int) (retErr error) {
	f, err := openEntry(r, rel, dir)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, f.Close()) }()
	return fdChown(f, uid, gid)
}

func entryChmod(r *os.Root, rel string, dir bool, mode fs.FileMode) (retErr error) {
	f, err := openEntry(r, rel, dir)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, f.Close()) }()
	return fdChmod(f, mode)
}
