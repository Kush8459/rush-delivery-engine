package config

import (
	"errors"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Env       string    `mapstructure:"env"`
	Postgres  Postgres  `mapstructure:"postgres"`
	Redis     Redis     `mapstructure:"redis"`
	Kafka     Kafka     `mapstructure:"kafka"`
	Services  Services  `mapstructure:"services"`
	RateLimit RateLimit `mapstructure:"rate_limit"`
	Auth      Auth      `mapstructure:"auth"`
	Telemetry Telemetry `mapstructure:"telemetry"`
}

type Telemetry struct {
	Enabled      bool   `mapstructure:"enabled"`
	OTLPEndpoint string `mapstructure:"otlp_endpoint"` // host:port, e.g. "localhost:4318"
}

type Auth struct {
	JWTSecret string `mapstructure:"jwt_secret"`
	TTLHours  int    `mapstructure:"ttl_hours"`
}

type RateLimit struct {
	OrdersPerMinute int `mapstructure:"orders_per_minute"`
}

type Postgres struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Name     string `mapstructure:"name"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
	SSLMode  string `mapstructure:"sslmode"`
}

type Redis struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type Kafka struct {
	Brokers []string `mapstructure:"brokers"`
	GroupID string   `mapstructure:"group_id"`
}

type Services struct {
	OrderPort    int `mapstructure:"order_port"`
	RiderPort    int `mapstructure:"rider_port"`
	LocationPort int `mapstructure:"location_port"`
	NotifyPort   int `mapstructure:"notify_port"`
}

// Load reads config from a YAML file and overlays env vars prefixed with DE_.
// Example: DE_POSTGRES_HOST=db.internal overrides postgres.host.
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	v.SetEnvPrefix("DE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	setDefaults(v)

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return nil, err
		}
	}

	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("env", "local")
	v.SetDefault("postgres.host", "localhost")
	v.SetDefault("postgres.port", 5432)
	v.SetDefault("postgres.name", "delivery_engine")
	v.SetDefault("postgres.user", "postgres")
	v.SetDefault("postgres.password", "postgres")
	v.SetDefault("postgres.sslmode", "disable")
	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.db", 0)
	v.SetDefault("kafka.brokers", []string{"localhost:9092"})
	v.SetDefault("kafka.group_id", "delivery-engine")
	v.SetDefault("services.order_port", 8080)
	v.SetDefault("services.rider_port", 8081)
	v.SetDefault("services.location_port", 8083)
	v.SetDefault("services.notify_port", 8085)
	v.SetDefault("rate_limit.orders_per_minute", 5)
	v.SetDefault("auth.jwt_secret", "dev-only-secret-change-me")
	v.SetDefault("auth.ttl_hours", 24)
	v.SetDefault("telemetry.enabled", true)
	v.SetDefault("telemetry.otlp_endpoint", "localhost:4318")
}
