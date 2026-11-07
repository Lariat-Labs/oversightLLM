package telemetry

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewParsesConnectionString(t *testing.T) {
	c, err := New("InstrumentationKey=abc-123;IngestionEndpoint=https://eastus2-3.in.applicationinsights.azure.com/;ApplicationId=x", "oversight", nil)
	if err != nil || c.ikey != "abc-123" || c.endpoint != "https://eastus2-3.in.applicationinsights.azure.com/v2.1/track" {
		t.Fatalf("parsed wrong: %+v %v", c, err)
	}
	if c, err := New("", "x", nil); c != nil || err != nil {
		t.Fatal("empty connection string should disable tracing")
	}
	if _, err := New("IngestionEndpoint=https://x", "x", nil); err == nil {
		t.Fatal("missing key should error")
	}
	var off *Client
	off.Record(Span{}) // nil client must be a no-op
	if off.Enabled() {
		t.Fatal("nil client reports enabled")
	}
}

func TestEnvelope(t *testing.T) {
	s := Span{TraceID: "t1", ID: "s1", ParentID: "p1", Name: "invoke_agent oversight-judge", Start: time.Unix(0, 0),
		Duration: 1500 * time.Millisecond, Success: true, Attributes: map[string]string{"gen_ai.agent.id": "oversight-judge"}}
	b, _ := json.Marshal(Envelope(s, "ab-cd", "oversight"))
	got := string(b)
	for _, want := range []string{`"ai.operation.id":"t1"`, `"ai.operation.parentId":"p1"`, `"type":"InProc"`, `"duration":"0.00:00:01.500000"`,
		`"name":"Microsoft.ApplicationInsights.abcd.RemoteDependency"`, `"gen_ai.agent.id":"oversight-judge"`, `"ai.cloud.role":"oversight"`} {
		if !strings.Contains(got, want) {
			t.Errorf("envelope missing %s\n%s", want, got)
		}
	}
	if formatDuration(26*time.Hour+3*time.Minute) != "1.02:03:00.000000" {
		t.Fatalf("duration = %s", formatDuration(26*time.Hour+3*time.Minute))
	}
}
