package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"ai-assistant/internal/aiclient"
	"ai-assistant/internal/config"
	"ai-assistant/internal/database"
	"ai-assistant/internal/domain"
	"ai-assistant/internal/logger"
	"ai-assistant/internal/migrator"
	"ai-assistant/internal/repository/postgres"
	"ai-assistant/internal/service"
	httptransport "ai-assistant/internal/transport/http"
	"go.uber.org/zap"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:])
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1) // run has already released the pool and flushed the logger.
	}
}

func command(args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	if len(args) == 2 && args[0] == "migrate" {
		switch args[1] {
		case "up", "down", "status", "version":
			return args[1], nil
		}
	}
	return "", errors.New("usage: server [migrate up|down|status|version]")
}

func run(ctx context.Context, args []string) error {
	cmd, err := command(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log, err := logger.New(cfg.Logger)
	if err != nil {
		return err
	}
	defer func() {
		// Stdout may not support fsync. Report Sync failures without panic and
		// without suppressing unrelated I/O errors.
		if err := log.Sync(); err != nil {
			fmt.Fprintf(os.Stderr, "logger sync: %v\n", err)
		}
	}()
	if err := allowCommand(cmd, cfg); err != nil {
		log.Error("migration command rejected", zap.Error(err))
		return err
	}
	if cmd == "" {
		if err := cfg.ValidateServer(); err != nil {
			log.Error("invalid HTTP configuration", zap.Error(err))
			return err
		}
	}
	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		log.Error("database connection failed", zap.Error(err))
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Error("database close failed", zap.Error(err))
		}
	}()
	log.Info("database connection established")
	migrationCtx, cancel := context.WithTimeout(ctx, cfg.MigrationTimeout)
	defer cancel()
	switch cmd {
	case "up":
		return migrator.Up(migrationCtx, db, log)
	case "down":
		if err := migrator.Down(migrationCtx, db, log); err != nil {
			log.Error("database migration failed", zap.Error(err))
			return err
		}
		return nil
	case "status":
		statuses, err := migrator.Status(migrationCtx, db)
		if err != nil {
			log.Error("database migration status failed", zap.Error(err))
			return err
		}
		for _, status := range statuses {
			log.Info("migration status", zap.Int64("migration_version", status.Source.Version),
				zap.String("state", string(status.State)), zap.String("source", status.Source.Path), zap.Time("applied_at", status.AppliedAt))
		}
		return nil
	case "version":
		version, err := migrator.Version(migrationCtx, db)
		if err != nil {
			log.Error("database migration version failed", zap.Error(err))
			return err
		}
		log.Info("database schema version", zap.Int64("migration_version", version))
		return nil
	}
	if cfg.AutoMigrate {
		if err := migrator.Up(migrationCtx, db, log); err != nil {
			return err
		}
	}
	if err := migrator.Check(migrationCtx, db); err != nil {
		log.Error("database schema incompatible", zap.Error(err))
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cancel() // Migration deadline must not govern the application's lifetime.
	log.Info("database schema verified")
	users := postgres.NewUserRepository(db)
	sessions := postgres.NewSessionRepository(db)
	bootstrapCtx, bootstrapCancel := context.WithTimeout(ctx, cfg.MigrationTimeout)
	err = service.BootstrapAdmin(bootstrapCtx, users, log, service.BootstrapConfig{
		Enabled: cfg.BootstrapAdmin, Username: cfg.AdminUsername, Password: cfg.AdminPassword,
	})
	bootstrapCancel()
	if err != nil {
		log.Error("administrator bootstrap failed", zap.Error(err))
		return err
	}
	auth, err := service.NewAuthService(users, sessions, log, service.AuthConfig{SessionTTL: cfg.SessionTTL})
	if err != nil {
		return err
	}
	storage, err := service.NewLocalDocumentStorage(cfg.DocumentStorageDir, cfg.DocumentMaxUploadBytes)
	if err != nil {
		return err
	}
	defer storage.Close()
	documentRepo, err := postgres.NewDocumentRepositoryWithIngestion(db, cfg.IngestionMaxAttempts)
	if err != nil {
		return err
	}
	jobs := postgres.NewIngestionRepository(db)
	backfillCtx, backfillCancel := context.WithTimeout(ctx, cfg.MigrationTimeout)
	count, err := jobs.BackfillUploaded(backfillCtx, cfg.IngestionMaxAttempts)
	backfillCancel()
	if err != nil {
		return err
	}
	log.Info("existing uploaded versions enqueued", zap.Int64("count", count))
	ingestion, err := service.NewIngestionService(jobs, cfg.IngestionMaxAttempts)
	if err != nil {
		return err
	}
	accessRepo := postgres.NewDocumentAccessRepository(db)
	access := service.NewDocumentAccessService(accessRepo, documentRepo, users, log)
	documents := service.NewDocumentService(documentRepo, access, storage, log)
	userService := service.NewUserService(users)
	search := service.NewSearchService(postgres.NewSearchRepository(db))
	chat := service.NewChatService(postgres.NewChatRepository(db), search, log)
	var aiClient *aiclient.Client
	if cfg.IngestionAvailable() || cfg.RAGEnabled || cfg.SemanticSearchEnabled || cfg.SemanticIndexingEnabled || cfg.AIServiceURL != "" && cfg.AIServiceToken != "" {
		aiClient, err = aiclient.New(aiclient.Config{URL: cfg.AIServiceURL, Token: cfg.AIServiceToken, Timeout: cfg.IngestionRequestTimeout, GenerateTimeout: cfg.RAGTimeout, EmbeddingTimeout: cfg.EmbeddingRequestTimeout})
		if err != nil {
			return err
		}
		defer aiClient.Close()
	}
	if cfg.SemanticSearchEnabled {
		search.EnableSemantic(postgres.NewSemanticSearchRepository(db), aiClient, aiClient, log, cfg.SemanticSupplementFTS, cfg.SemanticSearchTimeout)
	}
	if cfg.RAGEnabled {
		chat, err = service.NewChatServiceWithRAG(postgres.NewChatRepository(db), search, aiClient, log, service.RAGConfig{Timeout: cfg.RAGTimeout, MaxSources: cfg.RAGMaxSources, MaxHistoryMessages: cfg.RAGMaxHistoryMessages})
		if err != nil {
			return err
		}
	}
	var templateEngine service.TemplateEngine
	if aiClient != nil {
		templateEngine = aiClient
	}
	templates := service.NewTemplateService(postgres.NewTemplateRepository(db), documentRepo, access, storage, templateEngine, log)
	router, err := httptransport.NewRouter(auth, log, httptransport.RouterConfig{
		Production: cfg.Logger.Environment == "production", CookieSecure: cfg.CookieSecure, AllowedOrigins: cfg.AllowedOrigins,
	}, httptransport.Modules{Templates: templates, Users: userService, Documents: documents, Access: access, Ingestion: ingestion, Search: search, Chat: chat, MaxUploadBytes: cfg.DocumentMaxUploadBytes})
	if err != nil {
		return err
	}
	server := httptransport.NewServer(":"+strconv.Itoa(cfg.HTTPPort), router, log)
	server.WriteTimeout = max(server.WriteTimeout, 70*time.Second)
	if cfg.RAGEnabled {
		// The default 30s write deadline must not cut off a bounded 60s RAG
		// request before its search fallback or persisted response can be sent.
		server.WriteTimeout = max(server.WriteTimeout, cfg.RAGTimeout+10*time.Second)
	}
	if cfg.SemanticSearchEnabled {
		server.WriteTimeout = max(server.WriteTimeout, cfg.SemanticSearchTimeout+cfg.RAGTimeout+10*time.Second)
	}
	workerCtx, workerCancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	// Registered after storage/db defers: stop workers before closing dependencies.
	defer func() {
		workerCancel()
		workers.Wait()
	}()
	if cfg.SemanticIndexingEnabled {
		worker := service.NewSemanticWorker(postgres.NewEmbeddingRepository(db, domain.E5Identity()), aiClient, log, cfg.EmbeddingRequestTimeout)
		workers.Add(1)
		go func() { defer workers.Done(); worker.Run(workerCtx) }()
	}
	if cfg.IngestionAvailable() {
		worker, err := service.NewIngestionWorker(jobs, documentRepo, storage, aiClient, log, service.WorkerConfig{PollInterval: cfg.IngestionPollInterval, RequestTimeout: cfg.IngestionRequestTimeout})
		if err != nil {
			return err
		}
		for i := 0; i < cfg.IngestionWorkers; i++ {
			workers.Add(1)
			go func() { defer workers.Done(); worker.Run(workerCtx) }()
		}
		log.Info("ingestion workers started", zap.Int("workers", cfg.IngestionWorkers))
	} else {
		log.Info("ingestion workers disabled; jobs remain queued")
	}
	if err := httptransport.Serve(ctx, server, log, cfg.TLSCertFile, cfg.TLSKeyFile); err != nil {
		log.Error("HTTP server failed", zap.Error(err))
		return err
	}
	return nil
}

func allowCommand(cmd string, cfg config.Config) error {
	if cmd == "down" && cfg.Logger.Environment == "production" && !cfg.AllowProductionDown {
		return errors.New("migrate down is disabled in production; explicit ALLOW_PRODUCTION_MIGRATION_DOWN=true is required")
	}
	return nil
}
