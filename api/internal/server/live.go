package server

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/Lariat-Labs/oversightLLM/api/internal/activity"
	"github.com/Lariat-Labs/oversightLLM/api/internal/insights"
	"github.com/Lariat-Labs/oversightLLM/api/internal/metrics"
	"github.com/Lariat-Labs/oversightLLM/api/internal/stream"
)

// Live keeps a rolling window of GenAI spans in memory and publishes changes over SSE.
type Live struct {
	Insights *insights.Client
	Hub      *stream.Hub
	Lookback time.Duration
	Interval time.Duration
	MaxSpans int

	// OnRunFinished is called for each newly seen closing invoke_agent span.
	OnRunFinished func(conversationID, agent string)

	mu       sync.RWMutex
	spans    map[string]activity.Span
	lastPoll time.Time
	lastErr  error
	lastOK   time.Time
	ready    bool
}

// ingestionSlack re-reads recent minutes each poll because Log Analytics ingests late and out of order.
const ingestionSlack = 10 * time.Minute

func NewLive(ic *insights.Client, hub *stream.Hub, lookback, interval time.Duration, maxSpans int) *Live {
	return &Live{Insights: ic, Hub: hub, Lookback: lookback, Interval: interval, MaxSpans: maxSpans, spans: map[string]activity.Span{}}
}

func (l *Live) Run(ctx context.Context) {
	l.poll(ctx, true)
	t := time.NewTicker(l.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.poll(ctx, false)
		}
	}
}

func (l *Live) poll(ctx context.Context, initial bool) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	since := time.Time{}
	l.mu.RLock()
	if !initial && !l.lastPoll.IsZero() {
		since = l.newest().Add(-ingestionSlack)
	}
	l.mu.RUnlock()

	rows, err := l.Insights.Query(ctx, activity.ActivityQuery(since, l.MaxSpans), l.Lookback)
	metrics.Polls.Inc("outcome", metrics.Outcome(err))
	l.mu.Lock()
	l.lastPoll = time.Now().UTC()
	l.lastErr = err
	if err == nil {
		l.lastOK = l.lastPoll
	}
	if err != nil {
		l.mu.Unlock()
		slog.Warn("activity poll failed", "err", err)
		l.Hub.Publish(stream.Event{Type: "status", Data: l.StatusLocked()})
		return
	}
	var fresh []activity.Span
	for _, r := range rows {
		s := activity.SpanFromActivityRow(r)
		if _, seen := l.spans[s.ID]; !seen {
			fresh = append(fresh, s)
		}
		l.spans[s.ID] = s
	}
	cutoff := time.Now().Add(-l.Lookback)
	for id, s := range l.spans {
		if s.Time.Before(cutoff) {
			delete(l.spans, id)
		}
	}
	metrics.PolledSpans.Add(float64(len(fresh)))
	wasReady := l.ready
	l.ready = true
	l.mu.Unlock()

	if initial || !wasReady {
		slog.Info("activity loaded", "spans", len(rows))
		l.Hub.Publish(stream.Event{Type: "overview", Data: l.Overview(l.Lookback)})
		return
	}
	if len(fresh) == 0 {
		return
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].Time.Before(fresh[j].Time) })
	kinds := map[string]string{}
	for _, s := range activity.Normalize(l.Spans(l.Lookback)) {
		kinds[s.ID] = s.Kind
	}
	for _, s := range fresh {
		if k := kinds[s.ID]; k != "" {
			s.Kind = k
		}
		if s.Kind == "client" {
			continue
		}
		if s.Kind == "agent" && l.OnRunFinished != nil && s.ConversationID != "" {
			l.OnRunFinished(s.ConversationID, s.Agent)
		}
		l.Hub.Publish(stream.Event{
			Type: "activity", Agent: s.Agent, ConversationID: s.ConversationID,
			Data: activity.FeedItem{Time: s.Time, Agent: s.Agent, ConversationID: s.ConversationID, Kind: s.Kind, Text: activity.Describe(s), OK: !s.Failed()},
		})
	}
	l.Hub.Publish(stream.Event{Type: "overview", Data: l.Overview(l.Lookback)})
}

func (l *Live) newest() time.Time {
	var t time.Time
	for _, s := range l.spans {
		if s.Time.After(t) {
			t = s.Time
		}
	}
	return t
}

// Spans returns spans newer than now-window.
func (l *Live) Spans(window time.Duration) []activity.Span {
	l.mu.RLock()
	defer l.mu.RUnlock()
	cutoff := time.Now().Add(-window)
	out := make([]activity.Span, 0, len(l.spans))
	for _, s := range l.spans {
		if s.Time.After(cutoff) {
			out = append(out, s)
		}
	}
	return out
}

func (l *Live) Overview(window time.Duration) activity.Overview {
	if window <= 0 || window > l.Lookback {
		window = l.Lookback
	}
	return activity.BuildOverview(l.Spans(window), window, time.Now().UTC())
}

type LiveStatus struct {
	Ready    bool      `json:"ready"`
	Spans    int       `json:"spans"`
	LastPoll time.Time `json:"lastPoll"`
	Error    string    `json:"error,omitempty"`
	Interval string    `json:"interval"`
	Lookback string    `json:"lookback"`
}

func (l *Live) Status() LiveStatus {
	st := LiveStatus{Ready: l.ready, Spans: len(l.spans), LastPoll: l.lastPoll, Interval: l.Interval.String(), Lookback: l.Lookback.String()}
	if l.lastErr != nil {
		st.Error = l.lastErr.Error()
	}
	return st
}

func (l *Live) StatusLocked() LiveStatus {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.Status()
}
