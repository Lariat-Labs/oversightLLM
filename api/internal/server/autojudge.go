package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Lariat-Labs/oversightLLM/api/internal/judge"
	"github.com/Lariat-Labs/oversightLLM/api/internal/metrics"
	"github.com/Lariat-Labs/oversightLLM/api/internal/stream"
)

// Settings are runtime switches, persisted in the data dir.
type Settings struct {
	AutoJudge bool `json:"autoJudge"`
}

// Alert is raised when the auto-judge finds a run that went wrong.
type Alert struct {
	ConversationID string    `json:"conversationId"`
	Agent          string    `json:"agent"`
	Status         string    `json:"status"`
	Summary        string    `json:"summary"`
	RiskFlags      []string  `json:"riskFlags"`
	At             time.Time `json:"at"`
}

// alertStatuses are judge statuses worth a human look.
var alertStatuses = map[string]bool{"drifting": true, "stuck": true, "failed": true}

type autoJudge struct {
	mu       sync.Mutex
	settings Settings
	queue    chan string
	pending  map[string]bool
	alerts   []Alert
}

func (s *Server) settingsPath() string { return filepath.Join(s.Cfg.DataDir, "settings.json") }

// StartAutoJudge loads the switch state and starts the single judging worker.
// Runs are judged one at a time; with the switch off, nothing is queued.
func (s *Server) StartAutoJudge(ctx context.Context) {
	a := &s.auto
	a.settings = Settings{AutoJudge: s.Cfg.AutoJudge}
	if b, err := os.ReadFile(s.settingsPath()); err == nil {
		_ = json.Unmarshal(b, &a.settings)
	}
	a.queue = make(chan string, 100)
	a.pending = map[string]bool{}
	s.Live.OnRunFinished = s.enqueueJudge
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case id := <-a.queue:
				s.judgeRun(ctx, id)
			}
		}
	}()
	slog.Info("auto-judge", "enabled", a.settings.AutoJudge)
}

func (s *Server) enqueueJudge(conversationID, agent string) {
	if agent == judge.AgentID {
		metrics.AutoJudgeSkipped.Inc("reason", "self")
		return // never judge the judge: its own runs would trigger it again
	}
	a := &s.auto
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.settings.AutoJudge {
		metrics.AutoJudgeSkipped.Inc("reason", "disabled")
		return
	}
	if a.pending[conversationID] {
		metrics.AutoJudgeSkipped.Inc("reason", "duplicate")
		return
	}
	select {
	case a.queue <- conversationID:
		a.pending[conversationID] = true
	default:
		metrics.AutoJudgeSkipped.Inc("reason", "queue_full")
		slog.Warn("auto-judge queue full, skipping", "conversation", conversationID)
	}
}

func (s *Server) judgeRun(ctx context.Context, id string) {
	defer func() {
		s.auto.mu.Lock()
		delete(s.auto.pending, id)
		s.auto.mu.Unlock()
	}()
	if !s.currentSettings().AutoJudge {
		return // switched off while queued
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	c, err := s.conversation(ctx, id)
	if err != nil {
		slog.Warn("auto-judge: load conversation", "id", id, "err", err)
		return
	}
	in, err := s.Judge.AnalyzeConversation(ctx, c, false)
	if err != nil {
		slog.Warn("auto-judge: judge", "id", id, "err", err)
		return
	}
	s.Hub.Publish(stream.Event{Type: "intent", Agent: in.Agent, ConversationID: in.ConversationID, Data: in})
	if alertStatuses[in.Status] {
		s.raise(in)
	}
}

func (s *Server) raise(in judge.Intent) {
	al := Alert{ConversationID: in.ConversationID, Agent: in.Agent, Status: in.Status, Summary: in.Summary, RiskFlags: in.RiskFlags, At: time.Now().UTC()}
	s.auto.mu.Lock()
	s.auto.alerts = append([]Alert{al}, s.auto.alerts...)
	if len(s.auto.alerts) > 50 {
		s.auto.alerts = s.auto.alerts[:50]
	}
	s.auto.mu.Unlock()
	metrics.AutoJudgeAlerts.Inc("status", al.Status)
	slog.Info("judge alert", "agent", al.Agent, "status", al.Status, "conversation", al.ConversationID)
	s.Hub.Publish(stream.Event{Type: "alert", Agent: al.Agent, ConversationID: al.ConversationID, Data: al})
}

func (s *Server) currentSettings() Settings {
	s.auto.mu.Lock()
	defer s.auto.mu.Unlock()
	return s.auto.settings
}

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.currentSettings())
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	next := s.currentSettings()
	if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auto.mu.Lock()
	s.auto.settings = next
	s.auto.mu.Unlock()
	b, _ := json.MarshalIndent(next, "", "  ")
	_ = os.MkdirAll(s.Cfg.DataDir, 0o755)
	if err := os.WriteFile(s.settingsPath(), b, 0o600); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	slog.Info("settings changed", "autoJudge", next.AutoJudge)
	s.Hub.Publish(stream.Event{Type: "settings", Data: next})
	writeJSON(w, http.StatusOK, next)
}

func (s *Server) listAlerts(w http.ResponseWriter, _ *http.Request) {
	s.auto.mu.Lock()
	defer s.auto.mu.Unlock()
	out := append([]Alert{}, s.auto.alerts...)
	writeJSON(w, http.StatusOK, out)
}
