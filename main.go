// gitlab-ci-mr-pipelines-hooks 是一个 GitLab Merge Request Webhook 网关。
//
// 它监听 GitLab 的 merge_request 事件，当检测到 MR 从 Draft（草稿）
// 变为 Ready（就绪）时，自动调用 GitLab API 为该 MR 触发一次流水线，
// 从而让 Go CI、Flutter CI 等测试 Job 在"Mark as ready"时才开始执行。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/linfulongnet/gitlab-ci-mr-pipelines-hooks/internal/config"
	"github.com/linfulongnet/gitlab-ci-mr-pipelines-hooks/internal/gitlab"
	"github.com/linfulongnet/gitlab-ci-mr-pipelines-hooks/internal/webhook"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.FromEnv()
	if err != nil {
		logger.Error("配置加载失败", "err", err)
		os.Exit(1)
	}

	client := gitlab.New(cfg.GitLabBaseURL, cfg.GitLabToken, cfg.PipelineTimeout)
	handler := webhook.NewHandler(client, cfg.WebhookSecret, cfg.TriggerOnUpdate, logger)

	mux := http.NewServeMux()
	mux.Handle("/webhook", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// 优雅关闭
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("Webhook 网关启动",
			"listen_addr", cfg.ListenAddr,
			"gitlab_base_url", cfg.GitLabBaseURL,
			"trigger_on_update", cfg.TriggerOnUpdate,
			"secret_configured", cfg.WebhookSecret != "",
		)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP 服务异常退出", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("收到退出信号，正在关闭...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("关闭 HTTP 服务失败", "err", err)
	}
	logger.Info("网关已退出")
}
