package notify

import (
	"context"
	"testing"
)

// T6.3: with no SNS topic ARN configured, Notify logs only and never attempts an
// SNS call.
func TestNotify_NoTopicSkipsSNS(t *testing.T) {
	n := New(Config{})
	snsCalls := 0
	n.publish = func(ctx context.Context, topicARN, message string) error {
		snsCalls++
		return nil
	}

	if err := n.Notify(context.Background(), "price dropped"); err != nil {
		t.Fatalf("log-only Notify should not error: %v", err)
	}
	if snsCalls != 0 {
		t.Fatalf("no topic ARN should skip SNS entirely, got %d publish calls", snsCalls)
	}
}

// With a topic ARN configured, Notify routes to the publisher.
func TestNotify_WithTopicPublishes(t *testing.T) {
	n := New(Config{SNSTopicARN: "arn:aws:sns:us-east-1:123456789012:price-radar"})
	snsCalls := 0
	n.publish = func(ctx context.Context, topicARN, message string) error {
		snsCalls++
		return nil
	}

	if err := n.Notify(context.Background(), "price dropped"); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if snsCalls != 1 {
		t.Fatalf("configured topic should publish once, got %d", snsCalls)
	}
}
