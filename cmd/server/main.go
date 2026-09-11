package main

import (
	"context"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgxpool"
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
	log.Printf("sellingbot starting port=%s whatsapp=%v datadir=%s", cfg.Port, cfg.WhatsappEnabled, cfg.DataDir)
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatal(err)
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
			log.Fatalf("db connect: %v (set DATABASE_URL from the Postgres plugin)", err)
		}
		wait := time.Duration(attempt*2) * time.Second
		if wait > 20*time.Second {
			wait = 20 * time.Second
		}
		log.Printf("db connect (attempt %d): %v — retrying in %s", attempt, err, wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
	if err := runEmbeddedMigrations(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if err := db.SeedAdmin(ctx, pool, cfg.SeedEmail, cfg.SeedPassword); err != nil {
		log.Fatalf("seed: %v", err)
	}

	// Business timezone: DB setting wins, else TIMEZONE env, else IST.
	zoneName := cfg.Timezone
	var dbZone string
	if err := pool.QueryRow(ctx, `SELECT value FROM settings WHERE key='timezone'`).Scan(&dbZone); err == nil && dbZone != "" {
		zoneName = dbZone
	}
	if _, err := whatsapp.SetZone(zoneName); err != nil {
		log.Printf("bad timezone %q, using IST: %v", zoneName, err)
		whatsapp.SetZone("Asia/Kolkata")
	} else {
		log.Printf("business timezone: %s", whatsapp.ZoneName())
	}

	st, err := images.New(cfg.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	wa := whatsapp.New(pool, cfg.DataDir, cfg.WhatsappEnabled, cfg.DatabaseURL)
	go wa.Start(ctx)
	go scheduler.Followups(ctx, pool, wa)

	webHTTP := http.FS(webdist.FS)

	srv := &api.Server{Pool: pool, Secret: cfg.JWTSecret, WA: wa, Images: st, DataDir: cfg.DataDir, StartedAt: time.Now()}
	httpSrv := &http.Server{Addr: ":" + cfg.Port, Handler: srv.Router(webHTTP)}

	go func() {
		log.Printf("listening :%s", cfg.Port)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
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
