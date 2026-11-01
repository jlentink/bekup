package setup

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/jlentink/bekup/internal/certs"
	"github.com/jlentink/bekup/internal/imageref"
)

// Terminal is the operator's terminal. Prompts are only shown when
// IsTerminal is true (stdin is a TTY); passwords are read without echo.
type Terminal struct {
	In         *bufio.Reader
	Out        io.Writer
	IsTerminal bool
	// ReadPassword reads one line without echo (prompt already printed).
	ReadPassword func() ([]byte, error)
}

// maxAnswerLen bounds one typed answer.
const maxAnswerLen = 4096

// ErrNoTerminal is returned when an answer is missing and there is no
// terminal to ask on.
var ErrNoTerminal = errors.New("no terminal")

func (t *Terminal) line(prompt string) (string, error) {
	if t == nil || !t.IsTerminal {
		return "", ErrNoTerminal
	}
	if _, err := fmt.Fprint(t.Out, prompt); err != nil {
		return "", err
	}
	s, err := t.In.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || s == "") {
		return "", fmt.Errorf("reading the answer: %w", err)
	}
	if len(s) > maxAnswerLen {
		return "", errors.New("answer too long")
	}
	return strings.TrimSpace(s), nil
}

// Ask prompts until validate accepts the answer (an empty answer takes
// def when def is not empty).
func (t *Terminal) Ask(label, def string, validate func(string) error) (string, error) {
	prompt := label + ": "
	if def != "" {
		prompt = fmt.Sprintf("%s [%s]: ", label, def)
	}
	for range 5 {
		s, err := t.line(prompt)
		if err != nil {
			return "", err
		}
		if s == "" {
			s = def
		}
		if err := validate(s); err != nil {
			fmt.Fprintf(t.Out, "  %v\n", err)
			continue
		}
		return s, nil
	}
	return "", fmt.Errorf("%s: too many invalid answers", label)
}

// AskList prompts for a comma-separated list (empty = none).
func (t *Terminal) AskList(label string, validate func(string) error) ([]string, error) {
	for range 5 {
		s, err := t.line(label + " (comma-separated, empty for none): ")
		if err != nil {
			return nil, err
		}
		var out []string
		var bad error
		for _, item := range strings.Split(s, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if err := validate(item); err != nil {
				bad = err
				break
			}
			out = append(out, item)
		}
		if bad != nil {
			fmt.Fprintf(t.Out, "  %v\n", bad)
			continue
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s: too many invalid answers", label)
}

// NewPassphrase asks twice (no echo) for a new CA key passphrase.
func (t *Terminal) NewPassphrase() ([]byte, error) {
	if t == nil || !t.IsTerminal || t.ReadPassword == nil {
		return nil, ErrNoTerminal
	}
	for range 3 {
		fmt.Fprint(t.Out, "New CA key passphrase (min 16 characters): ")
		first, err := t.ReadPassword()
		if err != nil {
			return nil, err
		}
		if err := certs.ValidatePassphrase(first); err != nil {
			clear(first)
			fmt.Fprintf(t.Out, "  %v\n", err)
			continue
		}
		fmt.Fprint(t.Out, "Repeat the passphrase: ")
		second, err := t.ReadPassword()
		if err != nil {
			clear(first)
			return nil, err
		}
		same := bytes.Equal(first, second)
		clear(second)
		if !same {
			clear(first)
			fmt.Fprintln(t.Out, "  the passphrases do not match")
			continue
		}
		return first, nil
	}
	return nil, errors.New("no valid CA key passphrase entered")
}

// ExistingPassphrase asks once (no echo) for the passphrase of an existing
// CA key.
func (t *Terminal) ExistingPassphrase() ([]byte, error) {
	if t == nil || !t.IsTerminal || t.ReadPassword == nil {
		return nil, ErrNoTerminal
	}
	fmt.Fprint(t.Out, "Passphrase of the existing CA key: ")
	return t.ReadPassword()
}

// DBPassword asks twice (no echo) for the password of an existing database
// role (external database: existing roles, single account).
func (t *Terminal) DBPassword(role string) ([]byte, error) {
	if t == nil || !t.IsTerminal || t.ReadPassword == nil {
		return nil, ErrNoTerminal
	}
	for range 3 {
		fmt.Fprintf(t.Out, "Password of the database role %s (no echo): ", role)
		first, err := t.ReadPassword()
		if err != nil {
			return nil, err
		}
		fmt.Fprint(t.Out, "Repeat it: ")
		second, err := t.ReadPassword()
		if err != nil {
			clear(first)
			return nil, err
		}
		same := bytes.Equal(first, second)
		clear(second)
		if same {
			return first, nil
		}
		clear(first)
		fmt.Fprintln(t.Out, "  the passwords do not match")
	}
	return nil, errors.New("no matching password entered")
}

// Confirm requires the operator to type want exactly.
func (t *Terminal) Confirm(warning, want string) error {
	if t == nil || !t.IsTerminal {
		return fmt.Errorf("%w: this needs a typed confirmation on a terminal", ErrNoTerminal)
	}
	fmt.Fprintln(t.Out, warning)
	s, err := t.line(fmt.Sprintf("Type %q to continue: ", want))
	if err != nil {
		return err
	}
	if s != want {
		return errors.New("not confirmed")
	}
	return nil
}

// Given tells which answers came from flags (or the environment).
type Given struct {
	Image, Proxy, Domain, TLSMode, ACMEEmail, ACMEStaging, DockerGID, Targets, TrustedProxy bool
	// ComposeFirst: compose.yaml exists and decided Image and Variant;
	// only the deployment's own values are asked (D-205: domain, ACME
	// e-mail for the bundled proxy, Docker GID), the rest comes from
	// flags or defaults.
	ComposeFirst bool
	// Database: the database mode is decided (--database, or compose.yaml);
	// DBHost, DBCAFile, DBAuth, DBLocation, DBNetwork, DBTLS: those
	// external database flags were given.
	Database, DBHost, DBCAFile, DBAuth, DBLocation, DBNetwork, DBTLS bool
	// ProxyNetwork, ProxyContainer: the own-proxy join answers were given.
	ProxyNetwork, ProxyContainer bool
}

// Complete fills the answers that were not given: on a terminal by asking,
// otherwise (and always with nonInteractive) it fails and names the missing
// flags. imageRaw is the --image / BEKUP_IMAGE value (may be empty;
// ignored compose-first, where a.Image is set). a.Variant must be set.
func Complete(a *Answers, g Given, imageRaw string, t *Terminal, nonInteractive bool) error {
	var missing []string
	need := func(flag string) { missing = append(missing, flag) }
	ask := !nonInteractive && t != nil && t.IsTerminal
	if want := a.Variant.proxy(); want != "" && !g.Proxy {
		a.Proxy = want
		g.Proxy = true
	}
	if g.ComposeFirst {
		if a.Proxy == ProxyTraefik && !g.TLSMode {
			a.TLSMode, g.TLSMode = TLSACME, true
		}
		g.ACMEStaging, g.Targets = true, true
	}

	switch {
	case g.ComposeFirst:
	case imageRaw != "":
	case ask:
		s, err := t.Ask("bekup image (pinned: repo[:tag]@sha256:..., or "+imageref.OfficialRepo+":"+imageref.LatestTag+" to follow the releases)", "", func(s string) error { _, err := imageref.ParseBekup(s); return err })
		if err != nil {
			return err
		}
		imageRaw = s
	default:
		return pinHelp(errors.New("no image: pass --image or set BEKUP_IMAGE"))
	}
	if !g.ComposeFirst {
		ref, err := imageref.ParseBekup(imageRaw)
		if err != nil {
			return pinHelp(err)
		}
		a.Image = ref
	}

	if !g.Proxy {
		if ask {
			s, err := t.Ask("Reverse proxy (traefik, nginx, none)", string(ProxyTraefik), func(s string) error {
				switch Proxy(s) {
				case ProxyTraefik, ProxyNginx, ProxyNone:
					return nil
				default:
					return errors.New("traefik, nginx or none")
				}
			})
			if err != nil {
				return err
			}
			a.Proxy = Proxy(s)
		} else {
			need("--proxy")
		}
	}
	if !g.Domain {
		if ask {
			s, err := t.Ask("Public host name of the UI (e.g. backup.example.com)", "", ValidateDomain)
			if err != nil {
				return err
			}
			a.Domain = s
		} else {
			need("--domain")
		}
	}
	if a.Proxy == ProxyTraefik || a.Proxy == ProxyNginx {
		if !g.TLSMode {
			if ask {
				s, err := t.Ask("Proxy TLS (acme = Let's Encrypt, static = your own certificate)", string(TLSACME), func(s string) error {
					if TLSMode(s) == TLSACME || TLSMode(s) == TLSStatic {
						return nil
					}
					return errors.New("acme or static")
				})
				if err != nil {
					return err
				}
				a.TLSMode = TLSMode(s)
			} else {
				need("--tls-mode")
			}
		}
		if a.TLSMode == TLSACME && !g.ACMEEmail {
			if ask {
				s, err := t.Ask("ACME account e-mail", "", ValidateEmail)
				if err != nil {
					return err
				}
				a.ACMEEmail = s
			} else {
				need("--acme-email")
			}
		}
		if a.TLSMode == TLSACME && !g.ACMEStaging && ask {
			s, err := t.Ask("Use the Let's Encrypt staging CA while testing? (yes/no)", "no", func(s string) error {
				if s == "yes" || s == "no" {
					return nil
				}
				return errors.New("yes or no")
			})
			if err != nil {
				return err
			}
			a.ACMEStaging = s == "yes"
		}
	}
	if a.Variant.OwnProxy() && a.ProxyMode != ProxyLink {
		if !g.ProxyNetwork && a.ProxyNetwork == "" {
			if ask {
				s, err := t.Ask("Docker network your reverse proxy is on (docker inspect <proxy> --format '{{json .NetworkSettings.Networks}}')", "", func(s string) error {
					return ValidateDockerNetwork("proxy-network", s)
				})
				if err != nil {
					return err
				}
				a.ProxyNetwork = s
			} else {
				need("--proxy-network")
			}
		}
		if !g.ProxyContainer && a.ProxyContainer == "" {
			if ask {
				s, err := t.Ask("Your reverse proxy's container name (docker ps)", "", func(s string) error {
					return ValidateContainerName("proxy-container", s)
				})
				if err != nil {
					return err
				}
				a.ProxyContainer = s
			} else {
				need("--proxy-container")
			}
		}
	}
	if a.Proxy == ProxyNone && !g.TrustedProxy {
		if ask {
			s, err := t.Ask("CIDR of your own reverse proxy on the frontend network (e.g. 10.213.250.5/32)", "", func(s string) error {
				return checkScopedPrefix("trusted-proxy", s)
			})
			if err != nil {
				return err
			}
			a.TrustedProxies = []string{s}
		} else {
			need("--trusted-proxy")
		}
	}
	if !g.DockerGID {
		if ask {
			s, err := t.Ask("Group ID of the Docker socket on the host (stat -c %g /var/run/docker.sock)", "", func(s string) error {
				n, err := strconv.Atoi(s)
				if err != nil || n < 0 || n > maxGID {
					return errors.New("a numeric group ID")
				}
				return nil
			})
			if err != nil {
				return err
			}
			if a.DockerGID, err = strconv.Atoi(s); err != nil {
				return err
			}
		} else {
			need("--docker-gid")
		}
	}
	if err := completeDB(a, g, t, ask, need); err != nil {
		return err
	}
	if !g.Targets && ask {
		cidrs, err := t.AskList("Target database networks the runner may reach (CIDRs)", func(s string) error {
			return checkScopedPrefix("target-cidr", s)
		})
		if err != nil {
			return err
		}
		hosts, err := t.AskList("Target database host names", func(s string) error {
			b := Answers{TargetHosts: []string{s}}
			return validateTargetHosts(b.TargetHosts)
		})
		if err != nil {
			return err
		}
		a.TargetCIDRs, a.TargetHosts = cidrs, hosts
	}
	if len(missing) > 0 {
		why := "--non-interactive"
		if !nonInteractive {
			why = "no terminal"
		}
		return fmt.Errorf("%s: missing %s", why, strings.Join(missing, ", "))
	}
	return nil
}

// completeDB asks for (or names the flags of) the external database
// answers: host and CA bundle are required, the authentication defaults to
// bootstrap, single-account needs the account.
func completeDB(a *Answers, g Given, t *Terminal, ask bool, need func(string)) error {
	if !g.Database && a.DB.Mode == "" && ask {
		s, err := t.Ask("App database (bundled = bekup's own postgres service, external = your PostgreSQL)", string(DBBundled), func(s string) error {
			_, err := ParseDBMode(s)
			return err
		})
		if err != nil {
			return err
		}
		a.DB.Mode = DBMode(s)
	}
	if a.DB.Mode != DBExternal {
		return nil
	}
	if !g.DBLocation && a.DB.Location == "" && ask {
		s, err := t.Ask("Is your PostgreSQL a container on this Docker host? (docker = yes, remote = another server)", string(DBRemote), func(s string) error {
			_, err := ParseDBLocation(s)
			return err
		})
		if err != nil {
			return err
		}
		a.DB.Location = DBLocation(s)
	}
	if a.DB.Location == DBDocker && !g.DBNetwork && a.DB.Network == "" {
		if ask {
			s, err := t.Ask("Docker network of the database container", "", func(s string) error {
				return ValidateDockerNetwork("db-network", s)
			})
			if err != nil {
				return err
			}
			a.DB.Network = s
		} else {
			need("--db-network")
		}
	}
	if !g.DBHost && a.DB.Host == "" {
		label := "Host name or IP address of your PostgreSQL"
		if a.DB.Location == DBDocker {
			label = "The database container's name or network alias"
		}
		if ask {
			s, err := t.Ask(label, "", func(s string) error {
				d := Database{Mode: DBExternal, Host: s, CAFile: "x"}
				return d.validate()
			})
			if err != nil {
				return err
			}
			a.DB.Host = s
		} else {
			need("--db-host")
		}
	}
	if !g.DBTLS && a.DB.TLS == "" && ask {
		choice, err := askDBTLS(t)
		if err != nil {
			return err
		}
		a.DB.TLS = choice
	}
	if !g.DBCAFile && a.DB.CAFile == "" && a.DB.TLS.OrDefault() == DBTLSVerifyCAFile {
		if ask {
			s, err := t.Ask("CA bundle (PEM) of its server certificate, a file in the deployment directory", "", func(s string) error {
				return checkSourcePath("db-ca-file", s)
			})
			if err != nil {
				return err
			}
			a.DB.CAFile = s
		} else {
			need("--db-ca-file")
		}
	}
	if !g.DBAuth && a.DB.Auth == "" && ask {
		s, err := t.Ask("Roles: bootstrap (an admin creates them; default), existing-roles, dba-sql (SQL for your DBA) or single-account (WEAKENED: one account for everything)", string(DBAuthBootstrap), func(s string) error {
			_, err := ParseDBAuth(s)
			return err
		})
		if err != nil {
			return err
		}
		a.DB.Auth = DBAuth(s)
	}
	if a.DB.Auth == DBAuthSingleAccount && a.DB.User == "" {
		if ask {
			s, err := t.Ask("The single account's role name", "", func(s string) error {
				d := Database{Mode: DBExternal, Host: "x", CAFile: "x", Auth: DBAuthSingleAccount, User: s}
				return d.validate()
			})
			if err != nil {
				return err
			}
			a.DB.User = s
		} else {
			need("--db-user")
		}
	}
	return nil
}

// askDBTLS asks for the database TLS choice; a weak one (require,
// disable) is taken only after the warning and an explicit "yes", like
// the wizard (D-215), otherwise the choice is asked again.
func askDBTLS(t *Terminal) (DBTLS, error) {
	for range 5 {
		s, err := t.Ask("TLS: verify-ca-file (verify against your CA bundle; default), verify-system (a public CA), require (WEAKENED: not verified) or disable (WEAKENED: no TLS; prefer require, even on a private Docker network)", string(DBTLSVerifyCAFile), func(s string) error {
			_, err := ParseDBTLS(s)
			return err
		})
		if err != nil {
			return "", err
		}
		choice := DBTLS(s)
		if !choice.Weak() {
			if choice == DBTLSVerifyCAFile {
				return "", nil // the flag's default
			}
			return choice, nil
		}
		warn := DBTLSRequireWarning
		if choice == DBTLSDisable {
			warn = DBTLSDisableWarning
		}
		fmt.Fprintf(t.Out, "\nWARNING: --db-tls %s is an EXPLICIT WEAKENING (D-100): %s\n  bekup warns at every start, audits it and alerts every admin daily; the admin\n  pages show it. The printed bootstrap command needs --allow-insecure-admin-connection.\n", choice, warn)
		ok, err := t.Ask("Use it anyway? (yes/no)", "no", func(s string) error {
			if s == "yes" || s == "no" {
				return nil
			}
			return errors.New("yes or no")
		})
		if err != nil {
			return "", err
		}
		if ok == "yes" {
			return choice, nil
		}
	}
	return "", errors.New("TLS: too many answers without a choice")
}

// PinHelp explains how to obtain a digest-pinned reference.
func PinHelp(err error) error { return pinHelp(err) }

// pinHelp explains how to obtain a digest-pinned reference (and how to
// follow the releases instead).
func pinHelp(err error) error {
	return fmt.Errorf(`%w
The image must be pinned by digest. Get the digest of the image you deploy with:
  docker image inspect --format '{{index .RepoDigests 0}}' <repo>:<tag>
  (or: docker buildx imagetools inspect <repo>:<tag>)
and pass it as --image <repo>:<tag>@sha256:<digest> (or BEKUP_IMAGE);
to follow the releases instead (an explicit weakening), pass %s:%s
(other repositories and third-party images always need a digest, D-212)`, err, imageref.OfficialRepo, imageref.LatestTag)
}
