package config

import (
	"os"
	"strconv"
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
}

func Load() Config {
	c := Config{
		Port:         envOr("PORT", "8080"),
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		JWTSecret:    envOr("JWT_SECRET", "dev-secret-change-me"),
		DataDir:      envOr("DATA_DIR", "./data"),
		SeedEmail:    envOr("ADMIN_SEED_EMAIL", "admin@local.test"),
		SeedPassword: envOr("ADMIN_SEED_PASSWORD", "admin123"),
		Timezone:     envOr("TIMEZONE", "Asia/Kolkata"),
	}
	c.WhatsappEnabled = envBool("WHATSAPP_ENABLED", true)
	return c
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
