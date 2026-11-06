package summary

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/jlentink/bekup/internal/authz"
	"github.com/jlentink/bekup/internal/httpserver"
	"github.com/jlentink/bekup/internal/notify"
	"github.com/jlentink/bekup/internal/queue"
	"github.com/jlentink/bekup/internal/serviceutil"
)

// Preview rate limit: like the test sends, per user (an e-mail goes out
// with every preview), but a little stricter: one a minute, three at once.
const (
	previewPerMinute = 1
	previewBurst     = 3
)

// Options configure New.
type Options struct {
	DB notify.DB
	// Policy decides the recipient's view (job scope, staff parts) and
	// authorizes the API.
	Policy *authz.Policy
	// BaseURL is server.base_url (the dashboard link of the e-mail).
	BaseURL string
	// Defaults are notifications.summary_default_*; zero: DefaultDefaults.
	Defaults Defaults
	// PreviewLimiter is the per-user limit of sendSummaryPreview; nil: one
	// per minute, burst three.
	PreviewLimiter func(key string) (bool, time.Duration)
	Logger         *slog.Logger
	Now            func() time.Time
}

// Service is the periodic summary (D-226): the tick that emits due
// summaries, the content renderer of notify (Render) and the settings API.
type Service struct {
	o         Options
	dashboard string
	limiter   *serviceutil.Limiter
}

// New validates o.
func New(o Options) (*Service, error) {
	if o.DB == nil || o.Policy == nil {
		return nil, errors.New("summary: DB and Policy are required")
	}
	u, err := url.Parse(o.BaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil, errors.New("summary: BaseURL must be an absolute http(s) URL")
	}
	u.RawQuery, u.Fragment = "", ""
	u.Path = strings.TrimSuffix(u.Path, "/") + "/"
	if o.Defaults == (Defaults{}) {
		o.Defaults = DefaultDefaults()
	}
	if err := o.Defaults.Settings().Validate(); err != nil {
		return nil, fmt.Errorf("summary: defaults: %w", err)
	}
	allow := o.PreviewLimiter
	if allow == nil {
		rl, err := httpserver.NewRateLimiter[string](httpserver.RateLimitOptions{
			PerSecond: previewPerMinute / 60.0, Burst: previewBurst, MaxClients: 1000,
		})
		if err != nil {
			return nil, fmt.Errorf("summary: %w", err)
		}
		allow = rl.Allow
	}
	lim, err := serviceutil.NewLimiter(2, allow)
	if err != nil {
		return nil, fmt.Errorf("summary: %w", err)
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o, dashboard: u.String(), limiter: lim}, nil
}

// Defaults returns the configured defaults.
func (s *Service) Defaults() Defaults { return s.o.Defaults }

// Register registers the tick worker, enqueued every TickInterval by the
// leader.
func Register(ws *queue.Workers, s *Service) error {
	if err := queue.Register(ws, &TickWorker{s: s}); err != nil {
		return err
	}
	return ws.AddPeriodic(TickInterval, queue.NotifySummaryTickArgs{})
}
