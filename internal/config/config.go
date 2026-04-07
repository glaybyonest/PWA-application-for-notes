package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultAddr            = ":3000"
	defaultWebDir          = "web"
	defaultCertFile        = "cert/localhost.pem"
	defaultKeyFile         = "cert/localhost-key.pem"
	defaultVAPIDPublicKey  = "cert/vapid_public.key"
	defaultVAPIDPrivateKey = "cert/vapid_private.key"
	defaultVAPIDSubject    = "mailto:student@example.com"
)

// Config stores runtime settings for the local PWA server.
type Config struct {
	Addr                string
	WebDir              string
	CertFile            string
	KeyFile             string
	AllowHTTP           bool
	VAPIDPublicKeyPath  string
	VAPIDPrivateKeyPath string
	VAPIDSubject        string
}

// Load builds configuration from environment variables with sensible defaults.
func Load() Config {
	cfg := Config{
		Addr:                defaultAddr,
		WebDir:              defaultWebDir,
		CertFile:            defaultCertFile,
		KeyFile:             defaultKeyFile,
		AllowHTTP:           false,
		VAPIDPublicKeyPath:  defaultVAPIDPublicKey,
		VAPIDPrivateKeyPath: defaultVAPIDPrivateKey,
		VAPIDSubject:        defaultVAPIDSubject,
	}

	cfg.Addr = envString("APP_ADDR", cfg.Addr)
	cfg.WebDir = envString("APP_WEB_DIR", cfg.WebDir)
	cfg.CertFile = envString("APP_CERT_FILE", cfg.CertFile)
	cfg.KeyFile = envString("APP_KEY_FILE", cfg.KeyFile)
	cfg.AllowHTTP = envBool("APP_ALLOW_HTTP", cfg.AllowHTTP)
	cfg.VAPIDPublicKeyPath = envString("APP_VAPID_PUBLIC_KEY_PATH", cfg.VAPIDPublicKeyPath)
	cfg.VAPIDPrivateKeyPath = envString("APP_VAPID_PRIVATE_KEY_PATH", cfg.VAPIDPrivateKeyPath)
	cfg.VAPIDSubject = envString("APP_VAPID_SUBJECT", cfg.VAPIDSubject)

	return cfg
}

// WithBaseDir resolves all file system paths from a single base directory.
func (c Config) WithBaseDir(baseDir string) Config {
	c.WebDir = resolvePath(baseDir, c.WebDir)
	c.CertFile = resolvePath(baseDir, c.CertFile)
	c.KeyFile = resolvePath(baseDir, c.KeyFile)
	c.VAPIDPublicKeyPath = resolvePath(baseDir, c.VAPIDPublicKeyPath)
	c.VAPIDPrivateKeyPath = resolvePath(baseDir, c.VAPIDPrivateKeyPath)
	return c
}

func resolvePath(baseDir, value string) string {
	if value == "" {
		return value
	}
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(baseDir, value)
}

func envString(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}

	return parsed
}
