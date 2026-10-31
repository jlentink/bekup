package setup

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
	"go.yaml.in/yaml/v3"
)

// The compose files are checked on the model docker compose itself would
// run (D-205 amendment): compose-go, the loader of Docker Compose, reads
// compose.yaml and compose.override.yaml with every YAML document, merge
// key, alias, `!reset`/`!override` tag, `extends` and `${VAR}`
// interpolation (from the deployment's .env) applied, and the lint checks
// that resolved project, never the raw text.

// composeSrc is one compose file of the deployment.
type composeSrc struct {
	name string
	data []byte
}

// maxComposeNodes bounds compose-go's merge/reset walk, the document scan
// and the size of a document with every alias expanded (alias bombs).
const maxComposeNodes = 100000

// maxComposeAliases bounds the alias nodes (`*name`) of one document. The
// shipped files use about a dozen. compose-go's `!reset` walk compares
// every visit of an anchor with all earlier visits of it (quadratic: 40,000
// aliases of one anchor in a 120 KB file take about 20 s per load), so the
// count is bounded before compose-go sees the file.
const maxComposeAliases = 1000

// composeLoadTimeout bounds one compose-go load. With the checks of
// singleDocument a load takes milliseconds; the guard turns anything
// compose-go might still spin on into an error instead of a hang.
const composeLoadTimeout = time.Minute

// singleDocument refuses a compose file with more than one YAML document:
// docker compose merges every `---` document, so a second one could
// silently change what the first declares. It also refuses alias cycles,
// more than maxComposeAliases aliases and documents that expand to more
// than maxComposeNodes nodes (checkAliases).
func singleDocument(name string, data []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	n := 0
	for {
		var node yaml.Node
		err := dec.Decode(&node)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s is not valid YAML: %w", name, err)
		}
		n++
		if n > 1 {
			return fmt.Errorf("%s has more than one YAML document (`---`): docker compose merges them all; keep a single document", name)
		}
		if err := checkAliases(&node); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
}

// checkAliases walks one YAML document before compose-go resolves it. An
// alias inside the node it refers to (`x: &a {<<: *a}`, `x: &a [*a]`) is a
// cycle: compose-go's merge-key handling skips its cycle check and spins
// up to its visit limit (quadratic, seconds per load), and docker compose
// cannot load such a file either. Anchors are defined before their
// aliases, so a cycle is exactly an alias whose target is an ancestor.
// The alias count and the expanded size are bounded too.
func checkAliases(doc *yaml.Node) error {
	onPath := map[*yaml.Node]bool{}
	size := map[*yaml.Node]int{} // expanded size of finished nodes
	aliases := 0
	var walk func(n *yaml.Node) (int, error)
	walk = func(n *yaml.Node) (int, error) {
		if n == nil {
			return 0, nil
		}
		if n.Kind == yaml.AliasNode {
			aliases++
			if aliases > maxComposeAliases {
				return 0, fmt.Errorf("line %d: more than %d YAML aliases (*name); write the values out", n.Line, maxComposeAliases)
			}
			if n.Alias == nil {
				return 0, fmt.Errorf("line %d: the YAML alias *%s has no anchor", n.Line, bounded(n.Value))
			}
			if onPath[n.Alias] {
				return 0, fmt.Errorf("line %d: the YAML alias *%s is used inside its own anchor (a cycle); docker compose cannot load it", n.Line, bounded(n.Value))
			}
			s, ok := size[n.Alias]
			if !ok {
				// Not reachable from the document root before the alias
				// (yaml.v3 links only defined anchors): walk it once.
				var err error
				if s, err = walk(n.Alias); err != nil {
					return 0, err
				}
			}
			return s, nil
		}
		if s, ok := size[n]; ok {
			return s, nil
		}
		onPath[n] = true
		total := 1
		for _, c := range n.Content {
			s, err := walk(c)
			if err != nil {
				return 0, err
			}
			total += s
			if total > maxComposeNodes {
				return 0, fmt.Errorf("line %d: the document expands to more than %d YAML nodes (aliases included)", n.Line, maxComposeNodes)
			}
		}
		delete(onPath, n)
		size[n] = total
		return total, nil
	}
	_, err := walk(doc)
	return err
}

// errOtherComposeFile is returned for `include` and `extends.file`.
var errOtherComposeFile = errors.New("`include` and `extends.file` load other compose files (their images and settings are not checked); not allowed")

// denyLoader refuses every resource compose-go would load from outside
// the given documents (`include`, `extends.file`, remote resources). It
// comes before compose-go's own local loader.
type denyLoader struct{}

func (denyLoader) Accept(string) bool                           { return true }
func (denyLoader) Load(context.Context, string) (string, error) { return "", errOtherComposeFile }
func (denyLoader) Dir(string) string                            { return "" }

// lintRoot is the working directory every project is resolved in: a
// random absolute path no compose file can know, so only relative paths
// (./x, x) count as inside the deployment directory. An absolute path,
// even one naming the real directory (/deploy/x, /deploy/../deploy/x),
// stays outside: on the host it names something else.
var lintRoot = "/.bekup-lint-" + strings.ToLower(rand.Text())

// relInLintRoot returns the slash path of p below lintRoot ("" when p is
// not strictly inside).
func relInLintRoot(p string) string {
	if !insideDir(lintRoot, p) {
		return ""
	}
	rel, err := filepath.Rel(lintRoot, filepath.Clean(p))
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

// shownPath prints a resolved path for findings: paths below the
// deployment directory as ./rel, everything else as it is.
func shownPath(p string) string {
	if p == lintRoot {
		return "."
	}
	if rel := relInLintRoot(p); rel != "" {
		return bounded("./" + rel)
	}
	return bounded(p)
}

// loadProject resolves the documents (in docker compose's order: the
// compose file, then the override) like `docker compose` would with env
// as the project environment, in lintRoot (root only names the project).
// Every profile is enabled. Nothing outside the documents is read: no
// include/extends files, no env_file or label_file contents. setup has no
// request context; the load is bounded (MaxNodeVisits, no I/O), so it
// runs on its own background context.
func loadProject(root string, env map[string]string, docs ...composeSrc) (*types.Project, error) {
	return loadProjectMode(root, env, false, docs...)
}

// loadRawProject resolves the documents like loadProject but without
// interpolation: every ${VAR} stays text. Comparing it with the shipped
// file's raw project shows where a file uses variables, which the lint
// cannot see when they come from the host shell.
func loadRawProject(root string, docs ...composeSrc) (*types.Project, error) {
	return loadProjectMode(root, nil, true, docs...)
}

func loadProjectMode(root string, env map[string]string, raw bool, docs ...composeSrc) (p *types.Project, err error) {
	defer func() {
		if r := recover(); r != nil {
			p, err = nil, fmt.Errorf("the compose loader failed on this input: %v", r)
		}
	}()
	files := make([]types.ConfigFile, 0, len(docs))
	for _, d := range docs {
		if err := singleDocument(d.name, d.data); err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(d.data)) == 0 && len(files) > 0 {
			continue // an empty override changes nothing
		}
		files = append(files, types.ConfigFile{Filename: filepath.Join(lintRoot, d.name), Content: d.data})
	}
	environment := types.Mapping{}
	for k, v := range env {
		environment[k] = v
	}
	details := types.ConfigDetails{WorkingDir: lintRoot, ConfigFiles: files, Environment: environment}
	name := loader.NormalizeProjectName(filepath.Base(root))
	if name == "" {
		name = "bekup"
	}
	p, err = loadWithTimeout(details, func(o *loader.Options) {
		o.SetProjectName(name, false)
		o.Profiles = []string{"*"}
		o.SkipInterpolation = raw
		o.ResourceLoaders = []loader.ResourceLoader{denyLoader{}}
		o.SkipResolveEnvironment = true
		o.SkipResolveLabels = true
		o.MaxNodeVisits = maxComposeNodes
		loader.WithDiscardEnvFiles(o)
	})
	if err != nil {
		msg := strings.ReplaceAll(err.Error(), lintRoot+"/", "")
		return nil, fmt.Errorf("docker compose would not load it: %s", bounded(strings.ReplaceAll(msg, lintRoot, ".")))
	}
	return p, nil
}

// errComposeLoadTimeout is returned when compose-go does not finish within
// composeLoadTimeout.
var errComposeLoadTimeout = fmt.Errorf("resolving the file took longer than %s (too complex)", composeLoadTimeout)

// loadWithTimeout runs compose-go's loader with a deadline. The loader
// does no I/O here (denyLoader) and does not watch its context while it
// resolves `!reset`/merge keys, so the load runs in its own goroutine and
// a load past the deadline is an error. That goroutine ends on its own: the
// walk is bounded by MaxNodeVisits and the result channel is buffered.
func loadWithTimeout(details types.ConfigDetails, opts func(*loader.Options)) (*types.Project, error) {
	return loadWithin(composeLoadTimeout, func(ctx context.Context) (*types.Project, error) {
		return loader.LoadWithContext(ctx, details, opts)
	})
}

// loadWithin runs load with a deadline of d (see loadWithTimeout); a
// panic in load is an error.
func loadWithin(d time.Duration, load func(context.Context) (*types.Project, error)) (*types.Project, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	type result struct {
		p   *types.Project
		err error
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- result{err: fmt.Errorf("the compose loader failed on this input: %v", r)}
			}
		}()
		p, err := load(ctx)
		done <- result{p: p, err: err}
	}()
	select {
	case r := <-done:
		return r.p, r.err
	case <-ctx.Done():
		return nil, errComposeLoadTimeout
	}
}

var envLineRe = regexp.MustCompile(`^([A-Z][A-Z0-9_]{0,63})=([^\s"'$#\\` + "`" + `]*)$`)

// parseDotEnv parses .env: KEY=value lines (no quotes, `$`, spaces or
// escapes, so docker compose reads exactly the same values), comments and
// blank lines. It returns the 1-based numbers of the other lines.
func parseDotEnv(data []byte) (map[string]string, []int) {
	env := map[string]string{}
	var bad []int
	for i, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		m := envLineRe.FindStringSubmatch(l)
		if m == nil {
			bad = append(bad, i+1)
			continue
		}
		env[m[1]] = m[2]
	}
	return env, bad
}

var composeVarRe = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)`)

// projectEnv is the interpolation environment docker compose would use in
// this container: .env, overridden by the process environment for every
// variable the documents reference.
func projectEnv(dotenv map[string]string, lookup func(string) (string, bool), docs ...composeSrc) map[string]string {
	env := make(map[string]string, len(dotenv))
	for k, v := range dotenv {
		env[k] = v
	}
	if lookup == nil {
		return env
	}
	for _, d := range docs {
		for _, m := range composeVarRe.FindAllSubmatch(d.data, -1) {
			if v, ok := lookup(string(m[1])); ok {
				env[string(m[1])] = v
			}
		}
	}
	return env
}

// readDotEnv reads .env of the deployment (missing: empty) for the
// interpolation; a line docker compose could read differently is an error.
func readDotEnv(root string) (map[string]string, error) {
	data, err := readManaged(root, EnvFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	env, bad := parseDotEnv(data)
	if len(bad) > 0 {
		return nil, fmt.Errorf("%s line %d is not a plain KEY=value line (no quotes, spaces, `$` or escapes)", EnvFile, bad[0])
	}
	return env, nil
}

// allServices returns the enabled and disabled services, sorted by name.
func allServices(p *types.Project) []*types.ServiceConfig {
	out := make([]*types.ServiceConfig, 0, len(p.Services)+len(p.DisabledServices))
	for _, m := range []types.Services{p.Services, p.DisabledServices} {
		for name := range m {
			s := m[name]
			out = append(out, &s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
