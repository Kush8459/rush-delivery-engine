package kafka

import (
	"fmt"
	"net"
	"strconv"

	"github.com/segmentio/kafka-go"
)

// EnsureTopics idempotently creates any topic in `topics` that doesn't already
// exist on the cluster. Safe to call on every service boot — it no-ops for
// topics that are already present.
//
// Why this exists: docker-compose.yml has auto.create.topics.enable=true, but
// auto-creation only fires on first produce/consume, and kafka-go consumers that
// join a group before their topic exists end up with 0 partitions assigned
// (they don't re-subscribe when the topic later appears). Pre-creating at boot
// removes the ordering dependency.
func EnsureTopics(brokers []string, topics ...string) error {
	if len(brokers) == 0 {
		return fmt.Errorf("no brokers configured")
	}

	conn, err := kafka.Dial("tcp", brokers[0])
	if err != nil {
		return fmt.Errorf("dial broker: %w", err)
	}
	defer conn.Close()

	parts, err := conn.ReadPartitions()
	if err != nil {
		return fmt.Errorf("read partitions: %w", err)
	}
	existing := make(map[string]bool, len(parts))
	for _, p := range parts {
		existing[p.Topic] = true
	}

	missing := make([]kafka.TopicConfig, 0, len(topics))
	for _, t := range topics {
		if !existing[t] {
			missing = append(missing, kafka.TopicConfig{
				Topic:             t,
				NumPartitions:     1,
				ReplicationFactor: 1,
			})
		}
	}
	if len(missing) == 0 {
		return nil
	}

	controller, err := conn.Controller()
	if err != nil {
		return fmt.Errorf("get controller: %w", err)
	}
	controllerAddr := net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port))

	controllerConn, err := kafka.Dial("tcp", controllerAddr)
	if err != nil {
		return fmt.Errorf("dial controller: %w", err)
	}
	defer controllerConn.Close()

	if err := controllerConn.CreateTopics(missing...); err != nil {
		return fmt.Errorf("create topics: %w", err)
	}
	return nil
}
