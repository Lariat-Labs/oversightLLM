package server

import (
	"testing"

	"github.com/Lariat-Labs/oversightLLM/api/internal/judge"
)

func TestEnqueueJudgeRespectsSwitch(t *testing.T) {
	s := &Server{}
	s.auto.queue = make(chan string, 10)
	s.auto.pending = map[string]bool{}

	s.enqueueJudge("c1", "a")
	if len(s.auto.queue) != 0 {
		t.Fatal("switch off: nothing should be queued")
	}
	s.auto.settings.AutoJudge = true
	s.enqueueJudge("c1", "a")
	s.enqueueJudge("c1", "a") // duplicate while pending
	s.enqueueJudge("c2", "a")
	if len(s.auto.queue) != 2 {
		t.Fatalf("want 2 queued, got %d", len(s.auto.queue))
	}
	s.enqueueJudge("j1", judge.AgentID)
	if len(s.auto.queue) != 2 {
		t.Fatal("the judge's own runs must not be queued")
	}
}
