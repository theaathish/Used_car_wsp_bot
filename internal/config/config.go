package config

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Port            string
	DatabaseURL     string
	JWTSecret       string
	DataDir         string
	WhatsappEnabled bool
	SeedEmail       string
	SeedPassword    string
	Timezone        string
	// SeedPasswordGenerated is true when the seed password was auto-generated
	// (no ADMIN_SEED_PASSWORD provided). main prints it once on first boot.
	SeedPasswordGenerated bool
}

// Load reads configuration from environment variables. For one-click deploys
// nothing is strictly required: a missing JWT_SECRET is generated once and
// persisted to DATA_DIR (set JWT_SECRET to pin/rotate it manually), and a
// missing admin password is generated and printed once at first boot.
func Load() Config {
	c := Config{
		Port:         envOr("PORT", "8080"),
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		JWTSecret:    os.Getenv("JWT_SECRET"),
		DataDir:      envOr("DATA_DIR", "./data"),
		SeedEmail:    envOr("ADMIN_SEED_EMAIL", "admin@autokart.local"),
		SeedPassword: os.Getenv("ADMIN_SEED_PASSWORD"),
		Timezone:     envOr("TIMEZONE", "Asia/Kuala_Lumpur"),
	}
	c.WhatsappEnabled = envBool("WHATSAPP_ENABLED", true)

	if c.JWTSecret == "" {
		if s, err := loadOrCreateSecret(c.DataDir, "jwt.secret", 48); err == nil {
			c.JWTSecret = s
			log.Printf("JWT_SECRET not set — using generated secret persisted at %s/%s (set JWT_SECRET to pin it)", c.DataDir, "jwt.secret")
		} else {
			c.JWTSecret = randomHex(32)
			log.Printf("WARNING: JWT_SECRET not set and could not persist to DATA_DIR: %v — sessions reset on restart; set JWT_SECRET", err)
		}
	} else if len(c.JWTSecret) < 32 {
		log.Printf("WARNING: JWT_SECRET is shorter than 32 characters; tokens are weak")
	}
	if c.SeedPassword == "" {
		c.SeedPassword = randomHex(8) // 16 hex chars
		c.SeedPasswordGenerated = true
	}
	if c.DatabaseURL == "" {
		log.Printf("WARNING: DATABASE_URL is not set — the Railway template/IaC provisions Postgres and injects it automatically")
	}
	return c
}

func randomHex(nbytes int) string {
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// loadOrCreateSecret reads a hex secret file or creates it with 0600 perms.
func loadOrCreateSecret(dir, name string, nbytes int) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	if b, err := os.ReadFile(p); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s, nil
		}
	}
	s := randomHex(nbytes)
	return s, os.WriteFile(p, []byte(s), 0o600)
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func envBool(k string, d bool) bool {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return d
	}
	return b
}
