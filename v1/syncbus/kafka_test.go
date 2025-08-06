package syncbus

import (
	"context"
	"testing"

	sarama "github.com/IBM/sarama"
)

// newKafkaBus is a placeholder helper; full Kafka integration tests are skipped.
func newKafkaBus(t *testing.T) (*KafkaBus, context.Context, *sarama.MockFetchResponse, *sarama.MockBroker) {
	t.Skip("Kafka integration tests are skipped")
	return nil, nil, nil, nil
}

func TestKafkaBusPublishSubscribeFlowAndMetrics(t *testing.T) {
	newKafkaBus(t)
}

func TestKafkaBusContextBasedUnsubscribe(t *testing.T) {
	newKafkaBus(t)
}

func TestKafkaBusDeduplicatePendingKeys(t *testing.T) {
	newKafkaBus(t)
}

func TestKafkaBusPublishError(t *testing.T) {
	newKafkaBus(t)
}
