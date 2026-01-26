package ready

import (
	"context"
	"database/sql"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Checker pings DB and RabbitMQ for readiness.
type Checker struct {
	DB         *sql.DB
	RabbitMQURL string
}

// Ready returns nil if both DB and RabbitMQ are reachable.
func (c *Checker) Ready(ctx context.Context) error {
	if c.DB != nil {
		if err := c.DB.PingContext(ctx); err != nil {
			return fmt.Errorf("db ping: %w", err)
		}
	}
	if c.RabbitMQURL != "" {
		conn, err := amqp.Dial(c.RabbitMQURL)
		if err != nil {
			return fmt.Errorf("rabbitmq: %w", err)
		}
		_ = conn.Close()
	}
	return nil
}
