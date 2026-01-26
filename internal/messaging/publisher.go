package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Publisher publishes job commands to RabbitMQ.
type Publisher interface {
	PublishJob(ctx context.Context, job *JobCommand) error
}

// JobCommand message sent to workers.
type JobCommand struct {
	JobID         string          `json:"job_id"`
	Type          string          `json:"type"`
	UserID        string          `json:"user_id"`
	Payload       json.RawMessage `json:"payload"`
	CorrelationID string          `json:"correlation_id"`
}

// RabbitPublisher implements Publisher using RabbitMQ.
type RabbitPublisher struct {
	cfg    Config
	conn   *amqp.Connection
	ch     *amqp.Channel
	mu     sync.Mutex
	closed bool
}

// NewRabbitPublisher connects to RabbitMQ, declares cp.jobs exchange, returns publisher.
func NewRabbitPublisher(cfg Config) (*RabbitPublisher, error) {
	conn, err := amqp.Dial(cfg.RabbitMQURL)
	if err != nil {
		return nil, fmt.Errorf("amqp dial: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("amqp channel: %w", err)
	}
	if err := ch.ExchangeDeclare(cfg.JobsExchange, "topic", true, false, false, false, nil); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("declare exchange %s: %w", cfg.JobsExchange, err)
	}
	return &RabbitPublisher{cfg: cfg, conn: conn, ch: ch}, nil
}

// PublishJob publishes JobCommand to cp.jobs with routing key job.<type>.
func (p *RabbitPublisher) PublishJob(ctx context.Context, job *JobCommand) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return fmt.Errorf("publisher closed")
	}
	body, err := json.Marshal(job)
	if err != nil {
		return err
	}
	rk := "job." + job.Type
	err = p.ch.PublishWithContext(ctx, p.cfg.JobsExchange, rk, false, false, amqp.Publishing{
		ContentType:  "application/json",
		Body:         body,
		DeliveryMode: amqp.Persistent,
	})
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	slog.Debug("published job", "job_id", job.JobID, "type", job.Type, "routing_key", rk)
	return nil
}

// Close closes connection and channel.
func (p *RabbitPublisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	_ = p.ch.Close()
	return p.conn.Close()
}

// Ensure assignment-gen.jobs queue exists and is bound to cp.jobs with job.assignment-gen.
// Call this from a setup step or worker; control plane only publishes.
func EnsureAssignmentGenQueue(cfg Config) error {
	conn, err := amqp.Dial(cfg.RabbitMQURL)
	if err != nil {
		return err
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if err := ch.ExchangeDeclare(cfg.JobsExchange, "topic", true, false, false, false, nil); err != nil {
		return err
	}
	q, err := ch.QueueDeclare("assignment-gen.jobs", true, false, false, false, nil)
	if err != nil {
		return err
	}
	return ch.QueueBind(q.Name, "job.assignment-gen", cfg.JobsExchange, false, nil)
}

// EnsureResumeQueue ensures resume.jobs queue exists and is bound to cp.jobs with job.resume-gen and job.resume-optimize.
// Call this from a setup step or worker; control plane only publishes.
func EnsureResumeQueue(cfg Config) error {
	conn, err := amqp.Dial(cfg.RabbitMQURL)
	if err != nil {
		return err
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if err := ch.ExchangeDeclare(cfg.JobsExchange, "topic", true, false, false, false, nil); err != nil {
		return err
	}
	q, err := ch.QueueDeclare("resume.jobs", true, false, false, false, nil)
	if err != nil {
		return err
	}
	// Bind to both resume-gen and resume-optimize routing keys
	if err := ch.QueueBind(q.Name, "job.resume-gen", cfg.JobsExchange, false, nil); err != nil {
		return err
	}
	return ch.QueueBind(q.Name, "job.resume-optimize", cfg.JobsExchange, false, nil)
}
