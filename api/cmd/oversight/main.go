// Command oversight runs the agent oversight API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Lariat-Labs/oversightLLM/api/internal/activity"
	"github.com/Lariat-Labs/oversightLLM/api/internal/azauth"
	"github.com/Lariat-Labs/oversightLLM/api/internal/config"
	"github.com/Lariat-Labs/oversightLLM/api/internal/evals"
	"github.com/Lariat-Labs/oversightLLM/api/internal/foundry"
	"github.com/Lariat-Labs/oversightLLM/api/internal/insights"
	"github.com/Lariat-Labs/oversightLLM/api/internal/judge"
	"github.com/Lariat-Labs/oversightLLM/api/internal/server"
	"github.com/Lariat-Labs/oversightLLM/api/internal/stream"
	"github.com/Lariat-Labs/oversightLLM/api/internal/telemetry"
)

// Set at build time: go build -ldflags "-X main.version=1.2.3 -X main.commit=abc123".
var (
	version = "dev"
	commit  = ""
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("oversight %s %s\n", version, commit)
		return
	}
	for _, p := range []string{".env", "../.env"} {
		config.LoadDotEnv(p)
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	auth := azauth.NewCLI(cfg.AzCLIPath)
	acct, err := auth.Account(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	slog.Info("azure cli session", "user", acct.User, "subscription", acct.Subscription)

	httpc := &http.Client{Timeout: 3 * time.Minute}
	fc := &foundry.Client{ProjectEndpoint: cfg.ProjectEndpoint, OpenAIEndpoint: cfg.OpenAIEndpoint, Auth: auth, HTTP: httpc}
	ic := &insights.Client{WorkspaceID: cfg.WorkspaceID, Auth: auth, HTTP: httpc}
	store, err := evals.NewStore(cfg.DataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tracer, err := telemetry.New(cfg.AppInsightsCS, "oversight-api", auth)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	go tracer.Run(ctx)
	jd := judge.New(fc, cfg.JudgeDeployment, cfg.DataDir)
	jd.Tracer = tracer

	// `oversight register-judge` registers the judge in Foundry and exits.
	if len(os.Args) > 1 && os.Args[1] == "register-judge" {
		desc, meta := jd.Registration()
		v, err := fc.RegisterExternalAgent(ctx, judge.AgentID, judge.AgentID, desc, meta)
		if err != nil {
			fmt.Fprintln(os.Stderr, "register judge:", err)
			os.Exit(1)
		}
		fmt.Printf("registered %s version %s (otel_agent_id=%s)\n", v.Name, v.Version, judge.AgentID)
		return
	}

	hub := stream.NewHub()
	live := server.NewLive(ic, hub, cfg.Lookback, cfg.PollInterval, cfg.MaxSpans)
	srv := &server.Server{
		Cfg: cfg, Auth: auth, Foundry: fc, Insights: ic,
		Judge:     jd,
		Evals:     store,
		Publisher: &evals.Publisher{Foundry: fc, GraderModel: cfg.EvalDeployment},
		Hub:       hub, Live: live,
		Version: version, Commit: commit,
	}
	srv.StartAutoJudge(ctx)
	srv.RegisterGauges()
	go live.Run(ctx)

	hs := &http.Server{Addr: cfg.Host + ":" + cfg.Port, Handler: srv.Routes(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(shutdown)
	}()
	if cfg.WebDir != "" {
		slog.Info("serving web UI", "dir", cfg.WebDir)
	}
	slog.Info("oversight api listening", "version", version, "addr", "http://"+cfg.Host+":"+cfg.Port, "project", cfg.ProjectName(),
		"judge", cfg.JudgeDeployment, "judgeTraced", tracer.Enabled(), "poll", cfg.PollInterval, "lookback", cfg.Lookback, "runningWindow", activity.RunningWindow)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
