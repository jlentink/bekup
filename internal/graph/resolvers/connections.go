package resolvers

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/auth"
	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/connections"
	"github.com/jlentink/bekup/internal/engine"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
	"github.com/jlentink/bekup/internal/httpserver"
)

// Connection resolvers (task 2.11, D-135). Admins get the full non-secret
// configuration; maintainers get metadata only (settings stay null).
// Credentials are accepted as input and never returned: the model has only
// has* flags. Every runner call goes through the connections service.

var (
	errConnectionsNotConfigured = errors.New("resolvers: connection service not configured")
	errConnectionNotFound       = apierror.New(apierror.NotFound, "connection not found")
)

// connectionError maps service errors to API errors.
func connectionError(err error) error {
	var inUse *connections.InUseError
	var (
		fe *connections.FieldError
		nr *connections.NotReadyError
		rl *connections.RateLimitedError
		te *connections.TargetError
		pm *connections.PinMismatchError
		hm *connections.HostKeyMismatchError
	)
	switch {
	case err == nil:
		return nil
	case errors.As(err, &fe):
		return apierror.Invalid(fe.Field, fe.Message)
	case errors.As(err, &nr):
		return apierror.New(apierror.Conflict, "the connection is not ready: "+nr.Reason)
	case errors.As(err, &rl):
		return apierror.Newf(apierror.RateLimited, "too many connection checks; try again in %d seconds",
			httpserver.RetryAfterSeconds(rl.RetryAfter))
	case errors.As(err, &pm):
		return apierror.New(apierror.TLSPinMismatch,
			"the database's certificate does not match the pinned certificate; this was recorded as a security event. "+
				"If the certificate was replaced on purpose, fetch and confirm the new one")
	case errors.As(err, &hm):
		return apierror.New(apierror.SSHHostKeyMismatch,
			"the SSH bastion presented another host key than the pinned one; this was recorded as a security event. "+
				"If the key was replaced on purpose, fetch and confirm the new one")
	case errors.As(err, &te):
		return apierror.New(apierror.ConnectionFailed, te.Message)
	case errors.Is(err, connections.ErrRunnerUnavailable):
		return apierror.New(apierror.ConnectionFailed, "the runner is not reachable; try again later")
	case errors.Is(err, connections.ErrBusy):
		return errBusy
	case errors.Is(err, connections.ErrNotFound):
		return errConnectionNotFound
	case errors.Is(err, connections.ErrNameTaken):
		return apierror.New(apierror.Conflict, "another connection already uses this name")
	case errors.Is(err, connections.ErrConflict):
		return apierror.New(apierror.Conflict, "the connection was changed in the meantime; reload and try again")
	case errors.As(err, &inUse):
		return apierror.New(apierror.Conflict, "the connection is still used by these jobs: "+strings.Join(inUse.Jobs, ", "))
	case errors.Is(err, connections.ErrInUse):
		return apierror.New(apierror.Conflict, "the connection is still used by a job")
	case errors.Is(err, connections.ErrTooManyTemporary):
		return apierror.Newf(apierror.Conflict, "at most %d temporary restore connections can exist at a time; delete one first",
			connections.MaxActiveTemporary)
	case errors.Is(err, connections.ErrNoSSH):
		return apierror.New(apierror.Validation, "the connection has no SSH tunnel")
	default:
		return err
	}
}

// connectionCaller returns the service, the caller and its actor.
func (r *Resolver) connectionCaller(ctx context.Context) (*connections.Service, auth.Principal, connections.Actor, error) {
	if r.Connections == nil {
		return nil, auth.Principal{}, connections.Actor{}, errConnectionsNotConfigured
	}
	p, err := caller(ctx)
	if err != nil {
		return nil, auth.Principal{}, connections.Actor{}, err
	}
	// Only admins may act on temporary restore connections (task 4.4);
	// for everyone else they do not exist.
	return r.Connections, p, connections.Actor{
		UserID: p.UserID, IP: auth.RequestMetaFrom(ctx).IP, AllowTemporary: p.Role == auth.RoleAdmin,
	}, nil
}

func (r *Resolver) connections(ctx context.Context) ([]model.Connection, error) {
	svc, p, _, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	list, err := svc.List(ctx)
	if err != nil {
		return nil, connectionError(err)
	}
	out := make([]model.Connection, 0, len(list))
	for i := range list {
		out = append(out, *connectionModel(&list[i], p.Role == auth.RoleAdmin))
	}
	return out, nil
}

func (r *Resolver) connection(ctx context.Context, id uuid.UUID) (*model.Connection, error) {
	svc, p, _, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	c, err := svc.Get(ctx, id)
	if errors.Is(err, connections.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, connectionError(err)
	}
	return connectionModel(&c, p.Role == auth.RoleAdmin), nil
}

func (r *Resolver) engineVersions(ctx context.Context, eng *model.DatabaseEngine) ([]model.EngineVersion, error) {
	svc, _, _, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	out := []model.EngineVersion{}
	entries := svc.Catalog().Entries()
	for i := range entries {
		e := &entries[i]
		m := engineModel(e.Kind())
		if eng != nil && *eng != m {
			continue
		}
		out = append(out, model.EngineVersion{Engine: m, Key: e.Key(), ClientVersion: e.ClientVersion().String(), Legacy: e.Legacy()})
	}
	return out, nil
}

func (r *Resolver) createConnection(ctx context.Context, in model.CreateConnectionInput) (*model.Connection, error) {
	svc, _, actor, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	kind, ok := engineFromModel(in.Engine)
	if !ok {
		return nil, apierror.Invalid("engine", "unknown engine")
	}
	ci := connections.CreateInput{
		Name: in.Name, Engine: kind, VersionKey: in.VersionKey, ImageKey: derefString(in.ImageKey), Host: in.Host,
		Username: in.Username, Password: secretBytes(in.Password), ClientCertPEM: derefString(in.ClientCertificatePem),
		ClientKeyPEM: secretBytes(in.ClientKeyPem), SSH: sshInput(in.SSH), MaxConcurrentRuns: in.MaxConcurrentRuns,
	}
	if in.Port != nil {
		ci.Port = *in.Port
	}
	if in.TLSMode != nil {
		if ci.TLSMode, ok = tlsModeFromModel(*in.TLSMode); !ok {
			return nil, apierror.Invalid("tlsMode", "unknown TLS mode")
		}
		// Creating without pinned TLS is the same opt-out as a downgrade.
		if ci.TLSMode != connections.TLSPinned {
			if err := r.Authz.Authorize(ctx, authz.OpConnectionTLSDowngrade); err != nil {
				return nil, err
			}
		}
	}
	c, err := svc.Create(ctx, actor, ci)
	if err != nil {
		return nil, connectionError(err)
	}
	return connectionModel(&c, true), nil
}

func (r *Resolver) updateConnection(ctx context.Context, id uuid.UUID, in model.UpdateConnectionInput) (*model.Connection, error) {
	svc, _, actor, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	// Setting or removing credentials is its own operation (checked in
	// addition to connection.update, which already requires step-up).
	if in.Password != nil || in.ClientKeyPem != nil || isTrue(in.ClearPassword) || isTrue(in.ClearClientCertificate) || isTrue(in.DisableSSH) ||
		(in.SSH != nil && (in.SSH.Password != nil || in.SSH.PrivateKey != nil || in.SSH.PrivateKeyPassphrase != nil)) {
		if err := r.Authz.Authorize(ctx, authz.OpConnectionSecretUpdate); err != nil {
			return nil, err
		}
	}
	ui := connections.UpdateInput{
		Name: in.Name, VersionKey: in.VersionKey, ImageKey: in.ImageKey, Host: in.Host, Port: in.Port, Username: in.Username,
		Password: secretBytes(in.Password), ClearPassword: isTrue(in.ClearPassword),
		ClientCertPEM: in.ClientCertificatePem, ClientKeyPEM: secretBytes(in.ClientKeyPem),
		ClearClientCertificate: isTrue(in.ClearClientCertificate),
		SSH:                    sshInput(in.SSH), DisableSSH: isTrue(in.DisableSSH),
		MaxConcurrentRuns: in.MaxConcurrentRuns, ClearMaxConcurrentRuns: isTrue(in.ClearMaxConcurrentRuns),
	}
	c, err := svc.Update(ctx, actor, id, ui)
	if err != nil {
		return nil, connectionError(err)
	}
	return connectionModel(&c, true), nil
}

func (r *Resolver) deleteConnection(ctx context.Context, id uuid.UUID) (bool, error) {
	svc, _, actor, err := r.connectionCaller(ctx)
	if err != nil {
		return false, err
	}
	if err := svc.Delete(ctx, actor, id); err != nil {
		return false, connectionError(err)
	}
	return true, nil
}

func (r *Resolver) testConnection(ctx context.Context, id uuid.UUID) (*model.ConnectionTestResult, error) {
	svc, _, actor, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	res, err := svc.Test(ctx, actor, id)
	if err != nil {
		return nil, connectionError(err)
	}
	warnings := res.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	out := &model.ConnectionTestResult{
		Ok: res.OK, Code: nonEmpty(res.Code), Message: nonEmpty(res.Message), ServerVersion: nonEmpty(res.ServerVersion),
		DetectedVersionKey: nonEmpty(res.DetectedVersionKey), Compatible: res.Compatible, Legacy: res.Legacy,
		Warnings: warnings, DatabaseCount: res.Databases,
	}
	if res.Privileges.Status != "" {
		out.Privileges = targetPrivilegesModel(res.Privileges)
	}
	return out, nil
}

func (r *Resolver) detectVersion(ctx context.Context, id uuid.UUID) (*model.ConnectionVersionInfo, error) {
	svc, _, actor, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	v, err := svc.DetectVersion(ctx, actor, id)
	if err != nil {
		return nil, connectionError(err)
	}
	warnings := v.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	return &model.ConnectionVersionInfo{
		ServerVersion: v.ServerVersion, SuggestedVersionKey: nonEmpty(v.SuggestedKey), Legacy: v.Legacy,
		CurrentCompatible: v.CurrentCompatible, Warnings: warnings,
	}, nil
}

func (r *Resolver) listDatabases(ctx context.Context, id uuid.UUID) (*model.ConnectionDatabases, error) {
	svc, _, actor, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	dbs, warnings, err := svc.ListDatabases(ctx, actor, id)
	if err != nil {
		return nil, connectionError(err)
	}
	if dbs == nil {
		dbs = []string{}
	}
	if warnings == nil {
		warnings = []string{}
	}
	return &model.ConnectionDatabases{Databases: dbs, Warnings: warnings}, nil
}

func (r *Resolver) prePullImage(ctx context.Context, id uuid.UUID) (*model.ImagePrePullResult, error) {
	svc, _, actor, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	res, err := svc.PrePull(ctx, actor, id)
	if err != nil {
		return nil, connectionError(err)
	}
	return &model.ImagePrePullResult{ImageRef: res.ImageRef, AlreadyPresent: res.AlreadyPresent}, nil
}

func (r *Resolver) fetchCertificate(ctx context.Context, id uuid.UUID) (*model.CertificateChain, error) {
	svc, _, actor, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	ch, err := svc.FetchCertificate(ctx, actor, id)
	if err != nil {
		return nil, connectionError(err)
	}
	out := &model.CertificateChain{
		Certificates:   make([]model.ChainCertificate, 0, len(ch.Chain)),
		LeafSpkiSha256: base64.StdEncoding.EncodeToString(ch.LeafSPKISHA256),
	}
	if ch.SuggestedAnchorIndex >= 0 {
		i := ch.SuggestedAnchorIndex
		out.SuggestedAnchorIndex = &i
	}
	for i := range ch.Chain {
		c := &ch.Chain[i]
		dns, ips := c.DNSNames, c.IPAddresses
		if dns == nil {
			dns = []string{}
		}
		if ips == nil {
			ips = []string{}
		}
		out.Certificates = append(out.Certificates, model.ChainCertificate{
			Pem: c.PEM, Subject: c.Subject, Issuer: c.Issuer, DNSNames: dns, IPAddresses: ips,
			NotBefore: c.NotBefore, NotAfter: c.NotAfter, IsCa: c.IsCA, SelfSigned: c.SelfSigned,
			SpkiSha256: base64.StdEncoding.EncodeToString(c.SPKISHA256), SpkiSha256Hex: hex.EncodeToString(c.SPKISHA256),
			FingerprintSha256: c.Fingerprint,
		})
	}
	return out, nil
}

func pinInput(in model.CertificatePinInput) connections.PinInput {
	return connections.PinInput{
		CertIndex: in.CertIndex, CertificatePEM: derefString(in.CertificatePem), LeafSPKI: derefString(in.LeafSpkiSha256),
		AllowLeafRotation: isTrue(in.AllowLeafRotation),
	}
}

func (r *Resolver) confirmCertificatePin(ctx context.Context, id uuid.UUID, in model.CertificatePinInput) (*model.Connection, error) {
	svc, _, actor, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	c, err := svc.ConfirmPin(ctx, actor, id, pinInput(in))
	if err != nil {
		return nil, connectionError(err)
	}
	return connectionModel(&c, true), nil
}

func (r *Resolver) stageBackupPin(ctx context.Context, id uuid.UUID, in model.CertificatePinInput) (*model.Connection, error) {
	svc, _, actor, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	c, err := svc.StageBackupPin(ctx, actor, id, pinInput(in))
	if err != nil {
		return nil, connectionError(err)
	}
	return connectionModel(&c, true), nil
}

func (r *Resolver) fetchSSHHostKey(ctx context.Context, id uuid.UUID) (*model.SSHHostKey, error) {
	svc, _, actor, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	k, err := svc.FetchHostKey(ctx, actor, id)
	if err != nil {
		return nil, connectionError(err)
	}
	host, port := k.Host, k.Port
	return &model.SSHHostKey{Type: k.Type, FingerprintSha256: k.Fingerprint, AuthorizedKey: k.AuthorizedKey, Host: &host, Port: &port}, nil
}

func (r *Resolver) confirmSSHHostKey(ctx context.Context, id uuid.UUID, line, expectedHost string, expectedPort int) (*model.Connection, error) {
	svc, _, actor, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	c, err := svc.ConfirmHostKey(ctx, actor, id, line, expectedHost, expectedPort)
	if err != nil {
		return nil, connectionError(err)
	}
	return connectionModel(&c, true), nil
}

func (r *Resolver) setTLSMode(ctx context.Context, id uuid.UUID, mode model.ConnectionTLSMode) (*model.Connection, error) {
	svc, _, actor, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	m, ok := tlsModeFromModel(mode)
	if !ok {
		return nil, apierror.Invalid("mode", "unknown TLS mode")
	}
	// Any mode other than PINNED is an opt-out (D-001): its own operation,
	// checked before the service decides whether it is a downgrade.
	if m != connections.TLSPinned {
		if err := r.Authz.Authorize(ctx, authz.OpConnectionTLSDowngrade); err != nil {
			return nil, err
		}
	}
	c, err := svc.SetTLSMode(ctx, actor, id, m)
	if err != nil {
		return nil, connectionError(err)
	}
	return connectionModel(&c, true), nil
}

func (r *Resolver) generateConnectionSSHKeypair(ctx context.Context, id uuid.UUID) (*model.ConnectionSSHKeypair, error) {
	svc, _, actor, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	kp, c, err := svc.GenerateSSHKeypair(ctx, actor, id)
	if err != nil {
		return nil, connectionError(err)
	}
	return &model.ConnectionSSHKeypair{Connection: connectionModel(&c, true), PublicKey: kp.AuthorizedKey, FingerprintSha256: kp.Fingerprint}, nil
}

// --- temporary restore connections (task 4.4) -------------------------------

func (r *Resolver) temporaryConnections(ctx context.Context) ([]model.Connection, error) {
	svc, _, _, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	list, err := svc.ListTemporary(ctx)
	if err != nil {
		return nil, connectionError(err)
	}
	out := make([]model.Connection, 0, len(list))
	for i := range list {
		out = append(out, *connectionModel(&list[i], true))
	}
	return out, nil
}

func (r *Resolver) createTemporaryConnection(ctx context.Context, in model.CreateTemporaryConnectionInput) (*model.Connection, error) {
	svc, _, actor, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	kind, ok := engineFromModel(in.Engine)
	if !ok {
		return nil, apierror.Invalid("engine", "unknown engine")
	}
	ci := connections.CreateInput{
		Name: derefString(in.Name), Engine: kind, VersionKey: in.VersionKey, ImageKey: derefString(in.ImageKey), Host: in.Host,
		Username: in.Username, Password: secretBytes(in.Password), ClientCertPEM: derefString(in.ClientCertificatePem),
		ClientKeyPEM: secretBytes(in.ClientKeyPem), SSH: sshInput(in.SSH),
	}
	if in.Port != nil {
		ci.Port = *in.Port
	}
	if in.TLSMode != nil {
		if ci.TLSMode, ok = tlsModeFromModel(*in.TLSMode); !ok {
			return nil, apierror.Invalid("tlsMode", "unknown TLS mode")
		}
		// Creating without pinned TLS is the same opt-out as a downgrade.
		if ci.TLSMode != connections.TLSPinned {
			if err := r.Authz.Authorize(ctx, authz.OpConnectionTLSDowngrade); err != nil {
				return nil, err
			}
		}
	}
	c, err := svc.CreateTemporary(ctx, actor, ci)
	if err != nil {
		return nil, connectionError(err)
	}
	return connectionModel(&c, true), nil
}

func (r *Resolver) extendTemporaryConnection(ctx context.Context, id uuid.UUID, expiresAt time.Time) (*model.Connection, error) {
	svc, _, actor, err := r.connectionCaller(ctx)
	if err != nil {
		return nil, err
	}
	c, err := svc.ExtendTemporary(ctx, actor, id, expiresAt)
	if err != nil {
		return nil, connectionError(err)
	}
	return connectionModel(&c, true), nil
}

func (r *Resolver) deleteTemporaryConnection(ctx context.Context, id uuid.UUID) (bool, error) {
	svc, _, actor, err := r.connectionCaller(ctx)
	if err != nil {
		return false, err
	}
	if err := svc.DeleteTemporary(ctx, actor, id); err != nil {
		return false, connectionError(err)
	}
	return true, nil
}

// --- conversions -------------------------------------------------------------

func isTrue(b *bool) bool { return b != nil && *b }

func engineFromModel(e model.DatabaseEngine) (engine.Kind, bool) {
	switch e {
	case model.DatabaseEnginePostgres:
		return engine.KindPostgres, true
	case model.DatabaseEngineMysql:
		return engine.KindMySQL, true
	case model.DatabaseEngineMariadb:
		return engine.KindMariaDB, true
	default:
		return "", false
	}
}

func engineModel(k engine.Kind) model.DatabaseEngine {
	switch k {
	case engine.KindMySQL:
		return model.DatabaseEngineMysql
	case engine.KindMariaDB:
		return model.DatabaseEngineMariadb
	default:
		return model.DatabaseEnginePostgres
	}
}

func tlsModeFromModel(m model.ConnectionTLSMode) (connections.TLSMode, bool) {
	switch m {
	case model.ConnectionTLSModePinned:
		return connections.TLSPinned, true
	case model.ConnectionTLSModeUnverified:
		return connections.TLSUnverified, true
	case model.ConnectionTLSModeDisabled:
		return connections.TLSDisabled, true
	default:
		return "", false
	}
}

func tlsModeModel(m connections.TLSMode) model.ConnectionTLSMode {
	switch m {
	case connections.TLSUnverified:
		return model.ConnectionTLSModeUnverified
	case connections.TLSDisabled:
		return model.ConnectionTLSModeDisabled
	default:
		return model.ConnectionTLSModePinned
	}
}

func pinStatusModel(s connections.PinStatus) model.TLSPinStatus {
	switch s {
	case connections.PinMissing:
		return model.TLSPinStatusMissing
	case connections.PinValid:
		return model.TLSPinStatusValid
	case connections.PinExpires30d:
		return model.TLSPinStatusExpiresWithin30Days
	case connections.PinExpires7d:
		return model.TLSPinStatusExpiresWithin7Days
	case connections.PinExpired:
		return model.TLSPinStatusExpired
	default:
		return model.TLSPinStatusNotRequired
	}
}

func sshInput(in *model.ConnectionSSHInput) *connections.SSHInput {
	if in == nil {
		return nil
	}
	out := &connections.SSHInput{
		Host: in.Host, User: in.User, Password: secretBytes(in.Password), PrivateKey: secretBytes(in.PrivateKey),
		Passphrase: secretBytes(in.PrivateKeyPassphrase),
	}
	if in.Port != nil {
		out.Port = *in.Port
	}
	return out
}

func pinModel(p *connections.Pin) *model.CertificatePin {
	if p == nil {
		return nil
	}
	m := &model.CertificatePin{
		CertificatePem: p.CertificatePEM, Subject: p.Subject, Issuer: p.Issuer, IsCa: p.IsCA,
		NotAfter: p.NotAfter, PinnedAt: p.PinnedAt,
	}
	if p.LeafSPKI != nil {
		s := base64.StdEncoding.EncodeToString(p.LeafSPKI)
		m.LeafSpkiSha256 = &s
	}
	return m
}

// connectionModel converts c; full adds the non-secret settings (admins).
func connectionModel(c *connections.Connection, full bool) *model.Connection {
	m := &model.Connection{
		ID: c.ID, Name: c.Name, Engine: engineModel(c.Engine), VersionKey: c.VersionKey, LegacyVersion: c.Legacy,
		Host: c.Host, Port: c.Port, TLSMode: tlsModeModel(c.TLSMode), InsecureTransport: c.InsecureTransport,
		PinStatus: pinStatusModel(c.PinStatus), PinExpiresAt: c.PinExpiresAt, SSHEnabled: c.SSH != nil,
		Ready: c.Ready, NotReadyReason: nonEmpty(c.NotReadyReason), CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		MaxConcurrentRuns: c.MaxConcurrentRuns, EffectiveMaxConcurrentRuns: c.EffectiveMaxConcurrentRuns,
		Temporary: c.Temporary, ExpiresAt: c.ExpiresAt,
	}
	if !full {
		return m
	}
	set := &model.ConnectionSettings{
		ImageKey: c.ImageKey, Username: c.Username, HasPassword: c.HasPassword,
		ClientCertificatePem: nonEmpty(c.ClientCertPEM), TLSPin: pinModel(c.Pin), BackupPin: pinModel(c.BackupPin),
	}
	if s := c.SSH; s != nil {
		ss := &model.ConnectionSSHSettings{
			Host: s.Host, Port: s.Port, User: s.User, AuthMethod: model.ConnectionSSHAuthMethodNone,
			HasPassword: s.Auth == connections.SSHAuthPassword, HasPrivateKey: s.Auth == connections.SSHAuthPrivateKey,
			HasPassphrase: s.HasPassphrase, PublicKey: nonEmpty(s.PublicKey),
		}
		switch s.Auth {
		case connections.SSHAuthPassword:
			ss.AuthMethod = model.ConnectionSSHAuthMethodPassword
		case connections.SSHAuthPrivateKey:
			ss.AuthMethod = model.ConnectionSSHAuthMethodPrivateKey
		case connections.SSHAuthNone:
		}
		if s.HostKey != nil {
			at := s.HostKey.ConfirmedAt
			ss.HostKey = &model.SSHHostKey{
				Type: s.HostKey.Type, FingerprintSha256: s.HostKey.Fingerprint, AuthorizedKey: s.HostKey.AuthorizedKey, ConfirmedAt: &at,
			}
		}
		set.SSH = ss
	}
	m.Settings = set
	return m
}
