package contracts

import (
	"strconv"
	"time"
)

// Kafka topics. The suffix is the major version of the envelope and payloads on that topic:
// additive changes stay in v1, a breaking change means a new topic (v2) with both live during migration.
const (
	TopicPlan           = "loong.plan.v1"
	TopicDispatch       = "loong.dispatch.v1"
	TopicExecution      = "loong.execution.v1"
	TopicDriver         = "loong.driver.v1"
	TopicDriverPosition = "loong.driver.position.v1"
	TopicFleet          = "loong.fleet.v1"
	TopicSettings       = "loong.settings.v1"
	TopicCustomer       = "loong.customer.v1"
	TopicWallet         = "loong.wallet.v1"
)

const (
	dlqSuffix           = ".dlq"
	devPartitions int32 = 3
)

// DLQTopic is where a consumer parks records it could not process: the source topic plus ".dlq".
// The record keeps its key and value; headers carry the error and the consumer group (see eventbus).
func DLQTopic(topic string) string { return topic + dlqSuffix }

// CleanupPolicy is the Kafka `cleanup.policy` of a topic.
type CleanupPolicy string

const (
	// CleanupDelete drops records older than the retention.
	CleanupDelete CleanupPolicy = "delete"
	// CleanupCompact keeps the latest record per key: the topic is a changelog (last state wins).
	CleanupCompact CleanupPolicy = "compact"
)

// TopicSpec describes how a topic is created. The topics tool and tests read it, so the table below is
// the single source of truth for topic configuration.
type TopicSpec struct {
	Name       string
	Partitions int32
	Cleanup    CleanupPolicy
	// Retention of deletable records; zero keeps the broker default and is the only valid value for compacted topics.
	Retention time.Duration
}

// DLQRetention is how long poison records stay in a dead-letter topic before they are dropped.
const DLQRetention = 14 * 24 * time.Hour

// Topics returns the spec of every business topic (3 partitions: development sizing).
func Topics() []TopicSpec {
	return []TopicSpec{
		{Name: TopicPlan, Partitions: devPartitions, Cleanup: CleanupDelete, Retention: 7 * 24 * time.Hour},
		{Name: TopicDispatch, Partitions: devPartitions, Cleanup: CleanupDelete},
		{Name: TopicExecution, Partitions: devPartitions, Cleanup: CleanupDelete},
		{Name: TopicDriver, Partitions: devPartitions, Cleanup: CleanupDelete},
		{Name: TopicDriverPosition, Partitions: devPartitions, Cleanup: CleanupDelete, Retention: time.Hour},
		{Name: TopicFleet, Partitions: devPartitions, Cleanup: CleanupCompact},
		{Name: TopicSettings, Partitions: devPartitions, Cleanup: CleanupCompact},
		{Name: TopicCustomer, Partitions: devPartitions, Cleanup: CleanupDelete},
		{Name: TopicWallet, Partitions: devPartitions, Cleanup: CleanupDelete},
	}
}

// DLQ returns the spec of the dead-letter topic that pairs with s: same partitions so a record keeps its partition.
func (s TopicSpec) DLQ() TopicSpec {
	return TopicSpec{Name: DLQTopic(s.Name), Partitions: s.Partitions, Cleanup: CleanupDelete, Retention: DLQRetention}
}

// Configs returns the topic-level Kafka configs that differ from the broker defaults.
func (s TopicSpec) Configs() map[string]string {
	cfg := map[string]string{"cleanup.policy": string(s.Cleanup)}
	if s.Retention > 0 {
		cfg["retention.ms"] = strconv.FormatInt(s.Retention.Milliseconds(), 10)
	}
	return cfg
}

// AllTopics returns every business topic followed by its dead-letter topic.
func AllTopics() []TopicSpec {
	specs := Topics()
	for _, s := range Topics() {
		specs = append(specs, s.DLQ())
	}
	return specs
}
