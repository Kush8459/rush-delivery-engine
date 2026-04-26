package redisx

import (
	"context"
	"fmt"
	"time"

	"github.com/Kush8459/rush-delivery-engine/pkg/config"
	"github.com/redis/go-redis/v9"
)

// New creates a Redis client and verifies connectivity via PING.
func New(ctx context.Context, cfg config.Redis) (*redis.Client, error) {
	c := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := c.Ping(pingCtx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return c, nil
}
