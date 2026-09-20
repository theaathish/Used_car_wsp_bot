package main

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"sellingbot/internal/api"
	"sellingbot/internal/config"
	"sellingbot/internal/db"
	"sellingbot/internal/images"
	"sellingbot/internal/scheduler"
	"sellingbot/internal/whatsapp"
	migembed "sellingbot/migrations"
	webdist "sellingbot/web/dist"
)

func main() {
	cfg := config.Load()
	// Logger
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()
	if level, err := zerolog.ParseLevel(os.Getenv("LOG_LEVEL")); err == nil {
		logger = logger.Level(level)
	} else {
		logger = logger.Level(zerolog.InfoLevel)
	}
	version := os.Getenv("RAILWAY_GIT_COMMIT_SHA")
	if len(version) > 7 {
		version = version[:7]
	}
	if version == "" {
		version = "dev"
	}
	logger.Info().Msgf("sellingbot starting port=%s whatsapp=%v datadir=%s", cfg.Port, cfg.WhatsappEnabled, cfg.DataDir)
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		logger.Fatal().Err(err).Msg("")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Retry DB connect (Railway starts app + Postgres together; the DB
	// is often not ready on first attempt). Back off instead of crash-looping.
	var pool *pgxpool.Pool
	var err error
	for attempt := 1; ; attempt++ {
		pool, err = db.Connect(ctx, cfg.DatabaseURL)
		if err == nil {
			break
		}
		if attempt >= 30 {
			logger.Fatal().Msgf("db connect: %v (set DATABASE_URL from the Postgres plugin)", err)
		}
		wait := time.Duration(attempt*2) * time.Second
		if wait > 20*time.Second {
			wait = 20 * time.Second
		}
		logger.Info().Msgf("db connect (attempt %d): %v — retrying in %s", attempt, err, wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
	if err := runEmbeddedMigrations(ctx, pool); err != nil {
		logger.Fatal().Msgf("migrate: %v", err)
	}
	created, err := db.SeedAdmin(ctx, pool, cfg.SeedEmail, cfg.SeedPassword)
	if err != nil {
		logger.Fatal().Msgf("seed: %v", err)
	}
	if created && cfg.SeedPasswordGenerated {
		logger.Warn().Msgf("FIRST BOOT — admin created. Login: %s / %s  (change after first login; pin with ADMIN_SEED_EMAIL/ADMIN_SEED_PASSWORD)", cfg.SeedEmail, cfg.SeedPassword)
	} else if created {
		logger.Info().Msgf("admin seeded: %s", cfg.SeedEmail)
	}

	// Business timezone: DB setting wins, else TIMEZONE env, else IST.
	zoneName := cfg.Timezone
	var dbZone string
	if err := pool.QueryRow(ctx, `SELECT value FROM settings WHERE key='timezone'`).Scan(&dbZone); err == nil && dbZone != "" {
		zoneName = dbZone
	}
	if _, err := whatsapp.SetZone(zoneName); err != nil {
		logger.Info().Msgf("bad timezone %q, using IST: %v", zoneName, err)
		whatsapp.SetZone("Asia/Kuala_Lumpur")
	} else {
		logger.Info().Msgf("business timezone: %s", whatsapp.ZoneName())
	}

	st, err := images.New(cfg.DataDir)
	if err != nil {
		logger.Fatal().Err(err).Msg("")
	}
	wa := whatsapp.New(pool, cfg.DataDir, cfg.WhatsappEnabled, cfg.DatabaseURL)
	go wa.Start(ctx)
	go scheduler.Followups(ctx, pool, wa)

	webHTTP := http.FS(webdist.FS)

	srv := &api.Server{Pool: pool, Secret: cfg.JWTSecret, WA: wa, Images: st, DataDir: cfg.DataDir, StartedAt: time.Now(), Version: version, Logger: &logger}
	// Timeouts: a slow client must never hold a worker forever (Slowloris).
	// Write covers local-disk images + JSON; 60s is generous, not infinite.
	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Router(webHTTP),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		logger.Info().Msgf("listening :%s", cfg.Port)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("")
		}
	}()
	<-ctx.Done()
	shut, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	_ = httpSrv.Shutdown(shut)
	pool.Close()
}

func runEmbeddedMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	des, err := fs.ReadDir(migembed.FS, ".")
	if err != nil {
		return err
	}
	for _, d := range des {
		if d.IsDir() {
			continue
		}
		b, err := migembed.FS.ReadFile(d.Name())
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, string(b)); err != nil {
			return err
		}
	}
	return nil
}
