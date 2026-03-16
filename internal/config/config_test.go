package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// unsetEnvKeys temporarily unsets the given env vars for the duration of the test.
func unsetEnvKeys(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		old, exists := os.LookupEnv(k)
		_ = os.Unsetenv(k)
		k, old, exists := k, old, exists // capture loop variables
		if exists {
			t.Cleanup(func() { os.Setenv(k, old) })
		} else {
			t.Cleanup(func() { os.Unsetenv(k) })
		}
	}
}

func TestGetEnv_WithValue(t *testing.T) {
	t.Setenv("TEST_GETENV_KEY", "myvalue")
	assert.Equal(t, "myvalue", getEnv("TEST_GETENV_KEY", "default"))
}

func TestGetEnv_Fallback(t *testing.T) {
	unsetEnvKeys(t, "TEST_GETENV_NOT_SET")
	assert.Equal(t, "default", getEnv("TEST_GETENV_NOT_SET", "default"))
}

func TestGetEnvInt_ValidInt(t *testing.T) {
	t.Setenv("TEST_GETENVINT_VAL", "42")
	assert.Equal(t, 42, getEnvInt("TEST_GETENVINT_VAL", 10))
}

func TestGetEnvInt_InvalidString(t *testing.T) {
	t.Setenv("TEST_GETENVINT_BAD", "notanumber")
	assert.Equal(t, 10, getEnvInt("TEST_GETENVINT_BAD", 10))
}

func TestGetEnvInt_Missing(t *testing.T) {
	unsetEnvKeys(t, "TEST_GETENVINT_MISSING")
	assert.Equal(t, 99, getEnvInt("TEST_GETENVINT_MISSING", 99))
}

func TestLoad_Defaults(t *testing.T) {
	keys := []string{
		"APP_ENV", "DATABASE_URL", "SOURCE_DOMAIN",
		"SOURCE_ZH_URL", "SOURCE_EN_URL",
		"CRAWLER_PARALLELISM", "CRAWLER_DELAY_MS",
		"CRAWLER_RANDOM_DELAY_MS", "HTTP_TIMEOUT_SEC",
	}
	unsetEnvKeys(t, keys...)
	cfg := Load()
	assert.Equal(t, "development", cfg.AppEnv)
	assert.Equal(t, "", cfg.DBUrl)
	assert.Equal(t, "springbible.fhl.net", cfg.SourceDomain)
	assert.Equal(t, 5, cfg.CrawlerParallelism)
	assert.Equal(t, 200, cfg.CrawlerDelayMS)
	assert.Equal(t, 100, cfg.CrawlerRandomDelayMS)
	assert.Equal(t, 30, cfg.HTTPTimeoutSec)
}

func TestLoad_CustomValues(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost/db")
	t.Setenv("SOURCE_DOMAIN", "example.com")
	t.Setenv("SOURCE_ZH_URL", "http://example.com/zh/%d")
	t.Setenv("SOURCE_EN_URL", "http://example.com/en/%d")
	t.Setenv("CRAWLER_PARALLELISM", "10")
	t.Setenv("CRAWLER_DELAY_MS", "500")
	t.Setenv("CRAWLER_RANDOM_DELAY_MS", "250")
	t.Setenv("HTTP_TIMEOUT_SEC", "60")
	cfg := Load()
	assert.Equal(t, "production", cfg.AppEnv)
	assert.Equal(t, "postgres://user:pass@localhost/db", cfg.DBUrl)
	assert.Equal(t, "example.com", cfg.SourceDomain)
	assert.Equal(t, "http://example.com/zh/%d", cfg.SourceZHURL)
	assert.Equal(t, "http://example.com/en/%d", cfg.SourceENURL)
	assert.Equal(t, 10, cfg.CrawlerParallelism)
	assert.Equal(t, 500, cfg.CrawlerDelayMS)
	assert.Equal(t, 250, cfg.CrawlerRandomDelayMS)
	assert.Equal(t, 60, cfg.HTTPTimeoutSec)
}
