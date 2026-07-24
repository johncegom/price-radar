// Package notify delivers verdict/alert messages. When an SNS topic ARN is
// configured it publishes to SNS; when it is absent (pre-deployment on-demand
// use) it falls back to a log line only and never attempts an SNS call. Channel
// selection is driven purely by config presence, never by which trigger invoked
// the run.
//
// Only the config-presence fallback is built here; the full SNS notifier and
// verdict formatting are a later epic (E9).
package notify

import (
	"context"
	"errors"
	"log"
	"os"
)

// Config configures the notifier. An empty SNSTopicARN selects the log-only
// fallback.
type Config struct {
	SNSTopicARN string
}

// Notifier delivers messages to the configured channel.
type Notifier struct {
	topicARN string
	logger   *log.Logger
	// publish sends to SNS. It is nil until E9 wires the real SNS client; it is
	// only ever consulted when topicARN is set.
	publish func(ctx context.Context, topicARN, message string) error
}

// New returns a notifier for cfg.
func New(cfg Config) *Notifier {
	return &Notifier{
		topicARN: cfg.SNSTopicARN,
		logger:   log.New(os.Stderr, "", log.LstdFlags),
	}
}

// Notify delivers message. With no SNS topic configured it logs and returns nil
// without attempting any SNS call.
func (n *Notifier) Notify(ctx context.Context, message string) error {
	if n.topicARN == "" {
		n.logger.Printf("notify: %s", message)
		return nil
	}
	if n.publish == nil {
		// SNS is configured but the publisher is not wired yet (E9).
		return errors.New("notify: SNS publisher not configured")
	}
	return n.publish(ctx, n.topicARN, message)
}
