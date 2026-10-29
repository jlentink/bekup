package resolvers

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jlentink/bekup/internal/audit"
	"github.com/jlentink/bekup/internal/graph/apierror"
	"github.com/jlentink/bekup/internal/graph/model"
)

// AuditLister reads the audit log (*audit.Auditor).
type AuditLister interface {
	List(ctx context.Context, f audit.Filter, cursor int64, limit int) (audit.Page, error)
}

// auditCursorPrefix makes audit cursors opaque and distinct from cursors
// of other connections.
const auditCursorPrefix = "audit:"

// Input length limits of the audit filter (the audit_log column limits).
const (
	maxActionLen     = 128
	maxTargetTypeLen = 128
	maxTargetIDLen   = 256
)

func encodeAuditCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(auditCursorPrefix + strconv.FormatInt(id, 10)))
}

// decodeAuditCursor returns the id of an `after` cursor (0: none).
func decodeAuditCursor(after *string) (int64, error) {
	if after == nil || *after == "" {
		return 0, nil
	}
	errBad := apierror.Invalid("after", "invalid cursor")
	if len(*after) > 64 {
		return 0, errBad
	}
	raw, err := base64.RawURLEncoding.DecodeString(*after)
	if err != nil {
		return 0, errBad
	}
	s, ok := strings.CutPrefix(string(raw), auditCursorPrefix)
	if !ok {
		return 0, errBad
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != s {
		return 0, errBad
	}
	return id, nil
}

// pageSize validates `first` (1..audit.MaxListLimit, default
// audit.DefaultListLimit).
func pageSize(first *int) (int, error) {
	if first == nil {
		return audit.DefaultListLimit, nil
	}
	if *first < 1 || *first > audit.MaxListLimit {
		return 0, apierror.Invalid("first", "first must be between 1 and "+strconv.Itoa(audit.MaxListLimit))
	}
	return *first, nil
}

var actorKinds = map[model.AuditActorKind]audit.ActorKind{
	model.AuditActorKindUser:   audit.ActorUser,
	model.AuditActorKindSystem: audit.ActorSystem,
	model.AuditActorKindCli:    audit.ActorCLI,
}

func optString(field string, s *string, maxLen int) (string, error) {
	if s == nil {
		return "", nil
	}
	if *s == "" || !utf8.ValidString(*s) || utf8.RuneCountInString(*s) > maxLen {
		return "", apierror.Invalid(field, "must be 1 to "+strconv.Itoa(maxLen)+" characters")
	}
	return *s, nil
}

func auditFilter(in *model.AuditLogFilter) (audit.Filter, error) {
	var f audit.Filter
	if in == nil {
		return f, nil
	}
	action, err := optString("filter.action", in.Action, maxActionLen)
	if err != nil {
		return f, err
	}
	f.Action = audit.Action(action)
	if f.ActionPrefix, err = optString("filter.actionPrefix", in.ActionPrefix, maxActionLen); err != nil {
		return f, err
	}
	if f.TargetType, err = optString("filter.targetType", in.TargetType, maxTargetTypeLen); err != nil {
		return f, err
	}
	if f.TargetID, err = optString("filter.targetId", in.TargetID, maxTargetIDLen); err != nil {
		return f, err
	}
	f.ActorUserID = in.ActorUserID
	if in.ActorKind != nil {
		k, ok := actorKinds[*in.ActorKind]
		if !ok {
			return f, apierror.Invalid("filter.actorKind", "unknown actor kind")
		}
		f.ActorKind = k
	}
	if in.From != nil {
		f.From = *in.From
	}
	if in.To != nil {
		f.To = *in.To
	}
	if in.From != nil && in.To != nil && !f.From.Before(f.To) {
		return f, apierror.Invalid("filter.to", "to must be after from")
	}
	return f, nil
}

func modelActorKind(k audit.ActorKind) model.AuditActorKind {
	for m, a := range actorKinds {
		if a == k {
			return m
		}
	}
	// The audit_log CHECK allows only the three kinds; anything else is
	// shown as SYSTEM rather than failing the whole page.
	return model.AuditActorKindSystem
}

func auditConnection(page audit.Page, hasPrevious bool) *model.AuditLogConnection {
	conn := &model.AuditLogConnection{
		Edges: make([]model.AuditLogEdge, len(page.Entries)),
		PageInfo: &model.PageInfo{
			HasNextPage:     page.NextCursor > 0,
			HasPreviousPage: hasPrevious,
		},
	}
	for i := range page.Entries {
		e := &page.Entries[i]
		node := &model.AuditLogEntry{
			ID:          strconv.FormatInt(e.ID, 10),
			Ts:          e.TS,
			ActorKind:   modelActorKind(e.ActorKind),
			ActorUserID: e.ActorUserID,
			Action:      e.Action,
			TargetType:  e.TargetType,
			TargetID:    e.TargetID,
			Details:     string(e.Details),
			Hash:        hex.EncodeToString(e.Hash),
		}
		if node.Details == "" {
			node.Details = "{}"
		}
		if e.IP != nil {
			ip := e.IP.String()
			node.IP = &ip
		}
		conn.Edges[i] = model.AuditLogEdge{Cursor: encodeAuditCursor(e.ID), Node: node}
	}
	if n := len(conn.Edges); n > 0 {
		conn.PageInfo.StartCursor = &conn.Edges[0].Cursor
		conn.PageInfo.EndCursor = &conn.Edges[n-1].Cursor
	}
	return conn
}

// AuditChainSource reads the audit chain status (*audit.Auditor).
type AuditChainSource interface {
	ChainStatus(ctx context.Context) (audit.ChainStatus, error)
}

var errAuditChainNotConfigured = errors.New("resolvers: audit chain status not configured")

// auditChainStatus serves Query.auditChainStatus (admin, D-196).
func (r *Resolver) auditChainStatus(ctx context.Context) (*model.AuditChainStatus, error) {
	if r.AuditChain == nil {
		return nil, errAuditChainNotConfigured
	}
	st, err := r.AuditChain.ChainStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolvers: audit chain status: %w", err)
	}
	out := &model.AuditChainStatus{}
	if v := st.LastVerification; v != nil {
		m := &model.AuditChainVerification{CheckedAt: v.CheckedAt, Ok: v.OK, Full: v.Full}
		if v.Finding != "" {
			f := v.Finding
			m.Finding = &f
		}
		if v.VerifiedThroughID > 0 {
			id := strconv.FormatInt(v.VerifiedThroughID, 10)
			m.VerifiedThroughID = &id
		}
		out.LastVerification = m
	}
	if st.HeadID > 0 {
		id, hash, at := strconv.FormatInt(st.HeadID, 10), st.HeadHash, st.HeadAt
		out.HeadID, out.HeadHash, out.HeadAt = &id, &hash, &at
	}
	if h := st.SignedHead; h != nil {
		out.LatestSignedHead = &model.AuditSignedHead{
			ChainID: strconv.FormatInt(h.ChainID, 10), SignedAt: h.SignedAt, KeyID: h.KeyID,
		}
	}
	return out, nil
}
