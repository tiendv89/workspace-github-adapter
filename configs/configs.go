package configs

import (
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

var G *Config

type Config struct {
	Log    LogConfig    `mapstructure:"log"`
	API    APIConfig    `mapstructure:"api"`
	DB     DBConfig     `mapstructure:"db"`
	Redis  RedisConfig  `mapstructure:"redis"`
	GitHub GitHubConfig `mapstructure:"github"`
	Sync   SyncConfig   `mapstructure:"sync"`
}

type LogConfig struct {
	Level string `mapstructure:"level"`
}

type APIConfig struct {
	HTTP HTTPConfig `mapstructure:"http"`
}

type HTTPConfig struct {
	Address string `mapstructure:"address"`
	Mode    string `mapstructure:"mode"`
}

type DBConfig struct {
	Host                string `mapstructure:"host"`
	Port                int    `mapstructure:"port"`
	DBName              string `mapstructure:"db_name"`
	User                string `mapstructure:"user"`
	Password            string `mapstructure:"password"`
	ConnLifeTimeSeconds int    `mapstructure:"conn_life_time_seconds"`
	MaxIdleConns        int    `mapstructure:"max_idle_conns"`
	MaxOpenConns        int    `mapstructure:"max_open_conns"`
	LogLevel            int    `mapstructure:"log_level"`
}

func (c *DBConfig) DSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=disable",
		c.User, c.Password, c.Host, c.Port, c.DBName,
	)
}

type RedisConfig struct {
	InitAddress  []string `mapstructure:"init_address"`
	SelectDB     int      `mapstructure:"select_db"`
	Username     string   `mapstructure:"username"`
	Password     string   `mapstructure:"password"`
	DisableCache bool     `mapstructure:"disable_cache"`
}

func (c *RedisConfig) Addr() string {
	if len(c.InitAddress) == 0 {
		return "127.0.0.1:6379"
	}
	return c.InitAddress[0]
}

type GitHubConfig struct {
	Token         string `mapstructure:"token"`
	WebhookSecret string `mapstructure:"webhook_secret"`
}

type SyncConfig struct {
	StaleThresholdMinutes int `mapstructure:"stale_threshold_minutes"`
}

func (c *Config) StaleThreshold() time.Duration {
	m := c.Sync.StaleThresholdMinutes
	if m <= 0 {
		return 30 * time.Minute
	}
	return time.Duration(m) * time.Minute
}

// Init loads configuration into the global G variable. Called via cobra.OnInitialize.
func Init(path string) {
	v := viper.New()

	v.SetDefault("log.level", "info")
	v.SetDefault("api.http.address", ":8080")
	v.SetDefault("sync.stale_threshold_minutes", 30)

	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			log.Warn().Err(err).Msg("viper.ReadInConfig failed")
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		log.Err(err).Msg("viper.Unmarshal failed")
		panic(err)
	}
	G = &cfg
}

// Load reads configuration from the YAML file at path and applies environment-variable overrides.
// If the file does not exist, env-var values and defaults are used directly.
// Environment variables use underscores as separators; e.g. API_HTTP_ADDRESS overrides api.http.address.
func Load(path string) (*Config, error) {
	v := viper.New()

	v.SetDefault("log.level", "info")
	v.SetDefault("api.http.address", ":8080")
	v.SetDefault("sync.stale_threshold_minutes", 30)

	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		if !isNotFound(err) {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if cfg.DB.Host == "" {
		return nil, fmt.Errorf("db.host is required")
	}
	return &cfg, nil
}

func isNotFound(err error) bool {
	_, ok := err.(viper.ConfigFileNotFoundError)
	return ok || (err != nil && strings.Contains(err.Error(), "no such file"))
}
