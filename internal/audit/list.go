package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/jlentink/bekup/internal/db/sqlc"
)

// List page sizes.
const (
	DefaultListLimit = 50
	MaxListLimit     = 200
)

// Filter selects audit entries; zero fields do not filter. To is exclusive.
type Filter struct {
	Action Action
	// ActionPrefix matches an area, e.g. "auth." or "cli.admin.".
	ActionPrefix string
	ActorUserID  *uuid.UUID
	ActorKind    ActorKind
	TargetType   string
	TargetID     string
	From, To     time.Time
}

// ListEntry is one audit entry for display (GraphQL auditLog, task 1.2+).
type ListEntry struct {
	ID          int64
	TS          time.Time
	ActorKind   ActorKind
	ActorUserID *uuid.UUID
	Action      string
	TargetType  *string
	TargetID    *string
	IP          *netip.Addr
	Details     json.RawMessage
	Hash        []byte
}

// Page is one page of List, newest first. NextCursor is 0 on the last page.
type Page struct {
	Entries    []ListEntry
	NextCursor int64
}

// List returns entries matching f, newest first, with id < cursor (cursor
// 0: from the newest). limit is clamped to 1..MaxListLimit (0: default).
func (a *Auditor) List(ctx context.Context, f Filter, cursor int64, limit int) (Page, error) {
	switch {
	case limit <= 0:
		limit = DefaultListLimit
	case limit > MaxListLimit:
		limit = MaxListLimit
	}
	p := sqlc.ListAuditLogParams{
		ActionPrefix: nonEmpty(f.ActionPrefix),
		ActorUserID:  f.ActorUserID,
		ActorKind:    nonEmpty(string(f.ActorKind)),
		TargetType:   nonEmpty(f.TargetType),
		TargetID:     nonEmpty(f.TargetID),
		Action:       nonEmpty(string(f.Action)),
		MaxRows:      int32(limit + 1),
	}
	if cursor > 0 {
		p.BeforeID = &cursor
	}
	if !f.From.IsZero() {
		p.TsFrom = &f.From
	}
	if !f.To.IsZero() {
		p.TsTo = &f.To
	}
	rows, err := sqlc.New(a.db).ListAuditLog(ctx, p)
	if err != nil {
		return Page{}, fmt.Errorf("list audit log: %w", err)
	}
	var page Page
	if len(rows) > limit {
		rows = rows[:limit]
		page.NextCursor = rows[limit-1].ID
	}
	page.Entries = make([]ListEntry, len(rows))
	for i := range rows {
		r := &rows[i]
		page.Entries[i] = ListEntry{
			ID: r.ID, TS: r.Ts, ActorKind: ActorKind(r.ActorKind), ActorUserID: r.ActorUserID,
			Action: r.Action, TargetType: r.TargetType, TargetID: r.TargetID, IP: r.Ip,
			Details: r.Details, Hash: r.Hash,
		}
	}
	return page, nil
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
