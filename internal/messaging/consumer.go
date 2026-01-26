package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Consumer consumes result events and invokes ResultHandler.
type Consumer interface {
	Run(ctx context.Context) error
}

// ResultHandler processes result events (update job state).
type ResultHandler interface {
	HandleResult(ctx context.Context, event *ResultEvent) error
}

// ResultEvent message from workers.
type ResultEvent struct {
	JobID         string          `json:"job_id"`
	Type          string          `json:"type"`
	Status        string          `json:"status"`
	Result        json.RawMessage `json:"result,omitempty"` // Use RawMessage to handle both objects and arrays
	Error         *string         `json:"error,omitempty"`
	CorrelationID string          `json:"correlation_id,omitempty"`
}

// RabbitConsumer consumes from control-plane.results and calls ResultHandler.
type RabbitConsumer struct {
	cfg     Config
	handler ResultHandler
}

// NewRabbitConsumer returns a consumer that reads from control-plane.results.
func NewRabbitConsumer(cfg Config, handler ResultHandler) *RabbitConsumer {
	return &RabbitConsumer{cfg: cfg, handler: handler}
}

// Run declares topology, consumes until ctx.Done, acks after HandleResult.
func (c *RabbitConsumer) Run(ctx context.Context) error {
	conn, err := amqp.Dial(c.cfg.RabbitMQURL)
	if err != nil {
		return fmt.Errorf("amqp dial: %w", err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("amqp channel: %w", err)
	}
	defer ch.Close()

	if err := ch.ExchangeDeclare(c.cfg.ResultsExchange, "topic", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare results exchange: %w", err)
	}
	queue, err := ch.QueueDeclare(c.cfg.ResultsQueue, true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("declare results queue: %w", err)
	}
	if err := ch.QueueBind(queue.Name, c.cfg.ResultsBindKey, c.cfg.ResultsExchange, false, nil); err != nil {
		return fmt.Errorf("bind results queue: %w", err)
	}

	deliveries, err := ch.Consume(queue.Name, "control-plane", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}

	slog.Info("results consumer started", "queue", queue.Name, "exchange", c.cfg.ResultsExchange, "bind_key", c.cfg.ResultsBindKey)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return fmt.Errorf("deliveries closed")
			}
			slog.Debug("received result event", "routing_key", d.RoutingKey, "body_size", len(d.Body))
			var ev ResultEvent
			if err := json.Unmarshal(d.Body, &ev); err != nil {
				slog.Warn("invalid result event", "body", string(d.Body), "error", err, "routing_key", d.RoutingKey)
				_ = d.Nack(false, false)
				continue
			}
			slog.Info("processing result event", "job_id", ev.JobID, "type", ev.Type, "status", ev.Status)
			if err := c.handler.HandleResult(ctx, &ev); err != nil {
				slog.Error("handle result failed", "job_id", ev.JobID, "error", err)
				_ = d.Nack(false, true)
				continue
			}
			slog.Info("result event processed successfully", "job_id", ev.JobID)
			_ = d.Ack(false)
		}
	}
}

// RunWithRetry runs the consumer, reconnecting on connection loss.
func RunWithRetry(ctx context.Context, consumer *RabbitConsumer, backoff time.Duration) {
	slog.Info("starting results consumer with retry")
	for {
		err := consumer.Run(ctx)
		if ctx.Err() != nil {
			slog.Info("results consumer context cancelled")
			return
		}
		slog.Warn("results consumer stopped", "error", err, "retrying in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			slog.Info("retrying results consumer")
		}
	}
}
