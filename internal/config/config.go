package config

import (
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	HTTPAddr       string
	MetricsAddr    string
	DatabaseURL    string
	AppBaseURL     string
	SessionSecret  []byte
	AdminEmail     string
	AuthDevLogin   bool
	ViteDevOrigin  string
	SMTPHost       string
	SMTPPort       string
	SMTPUser       string
	SMTPPass       string
	SMTPFrom       string
	MediaDir       string
	CookieSecure   bool
	XAIAPIKey      string
	XAIModel       string
	XAIVisionModel string
	AIEnabled      bool
	MCPEnabled     bool
}

func Load() (Config, error) {
	c := Config{
		HTTPAddr:       env("HTTP_ADDR", ":8080"),
		MetricsAddr:    env("METRICS_ADDR", "127.0.0.1:9090"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		AppBaseURL:     strings.TrimRight(env("APP_BASE_URL", "http://localhost:8080"), "/"),
		AdminEmail:     strings.TrimSpace(os.Getenv("ADMIN_EMAIL")),
		AuthDevLogin:   truthy(os.Getenv("AUTH_DEV_LOGIN")),
		ViteDevOrigin:  strings.TrimRight(env("VITE_DEV_ORIGIN", "http://localhost:5173"), "/"),
		SMTPHost:       os.Getenv("SMTP_HOST"),
		SMTPPort:       env("SMTP_PORT", "587"),
		SMTPUser:       os.Getenv("SMTP_USER"),
		SMTPPass:       os.Getenv("SMTP_PASS"),
		SMTPFrom:       env("SMTP_FROM", "Gritual <noreply@gritual.fit>"),
		MediaDir:       env("MEDIA_DIR", "./data/media"),
		XAIAPIKey:      os.Getenv("XAI_API_KEY"),
		XAIModel:       env("XAI_MODEL", "grok-4.5"),
		XAIVisionModel: env("XAI_VISION_MODEL", "grok-4.5"),
	}

	u, err := url.Parse(c.AppBaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return Config{}, fmt.Errorf("APP_BASE_URL is invalid")
	}
	c.CookieSecure = strings.EqualFold(u.Scheme, "https")

	secret := os.Getenv("SESSION_SECRET")
	if len(secret) >= 32 {
		c.SessionSecret = []byte(secret)
	} else if c.AuthDevLogin {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return Config{}, fmt.Errorf("generate SESSION_SECRET: %w", err)
		}
		c.SessionSecret = buf
		slog.Warn("SESSION_SECRET missing or short; generated ephemeral secret (AUTH_DEV_LOGIN=1)")
	} else {
		return Config{}, fmt.Errorf("SESSION_SECRET must be at least 32 bytes (set AUTH_DEV_LOGIN=1 only for local)")
	}

	if c.AuthDevLogin {
		slog.Warn("AUTH_DEV_LOGIN enabled; never use in production")
	}
	if c.SMTPHost == "" {
		slog.Warn("SMTP unset; magic links will print to stdout")
	}
	if v, ok := os.LookupEnv("AI_ENABLED"); ok && strings.TrimSpace(v) != "" {
		c.AIEnabled = truthy(v)
	} else {
		c.AIEnabled = c.XAIAPIKey != ""
	}
	if v, ok := os.LookupEnv("MCP_ENABLED"); ok && strings.TrimSpace(v) != "" {
		c.MCPEnabled = truthy(v)
	} else {
		c.MCPEnabled = true
	}
	return c, nil
}

func (c Config) AllowedOrigins() []string {
	base := normalizeOrigin(c.AppBaseURL)
	out := []string{base}
	if c.allowViteOrigin() {
		v := normalizeOrigin(c.ViteDevOrigin)
		if v != "" && v != base {
			out = append(out, v)
		}
	}
	return out
}

func (c Config) allowViteOrigin() bool {
	return c.AuthDevLogin || isLocal(c.AppBaseURL)
}

func isLocal(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func normalizeOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(strings.TrimSpace(raw), "/")
	}
	return strings.ToLower(u.Scheme) + "://" + u.Host
}

func OriginAllowed(origin string, allowed []string) bool {
	got := normalizeOrigin(origin)
	if got == "" {
		return false
	}
	for _, a := range allowed {
		if got == normalizeOrigin(a) {
			return true
		}
	}
	return false
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
