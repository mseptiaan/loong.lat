// Command topics creates the Kafka topics the services use, with the configuration declared in
// pkg/contracts (partitions, cleanup policy, retention), plus each topic's dead-letter topic. It is safe to
// run repeatedly: existing topics keep their data and only have their configs brought in line.
//
//	go run ./cmd/topics                      # brokers from KAFKA_BROKERS, default localhost:19092
//	go run ./cmd/topics -brokers a:9092 -replication 3
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
)

func main() {
	defaultBrokers := "localhost:19092"
	if v := os.Getenv("KAFKA_BROKERS"); v != "" {
		defaultBrokers = v
	}
	brokers := flag.String("brokers", defaultBrokers, "comma-separated Kafka bootstrap servers")
	replication := flag.Int("replication", 1, "replication factor (1 for the single-node dev broker)")
	wait := flag.Duration("wait", 30*time.Second, "how long to wait for the broker to come up")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, log, strings.Split(*brokers, ","), int16(*replication), *wait); err != nil {
		log.Error("topics", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger, brokers []string, replication int16, wait time.Duration) error {
	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return err
	}
	defer client.Close()
	admin := kadm.NewClient(client)

	if err := waitForBroker(ctx, admin, wait); err != nil {
		return err
	}
	for _, spec := range contracts.AllTopics() {
		if err := ensure(ctx, log, admin, spec, replication); err != nil {
			return fmt.Errorf("%s: %w", spec.Name, err)
		}
	}
	return nil
}

func waitForBroker(ctx context.Context, admin *kadm.Client, wait time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		attempt, cancelAttempt := context.WithTimeout(ctx, 3*time.Second)
		_, err := admin.BrokerMetadata(attempt)
		cancelAttempt()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("broker not reachable within %v: %w", wait, err)
		case <-time.After(time.Second):
		}
	}
}

// ensure creates the topic, or, when it exists, sets its configs to the declared ones. Partition counts of an
// existing topic are never changed: that would reshuffle key-to-partition assignment and break ordering.
func ensure(ctx context.Context, log *slog.Logger, admin *kadm.Client, spec contracts.TopicSpec, replication int16) error {
	configs := map[string]*string{}
	var alter []kadm.AlterConfig
	for name, value := range spec.Configs() {
		configs[name] = kadm.StringPtr(value)
		alter = append(alter, kadm.AlterConfig{Op: kadm.SetConfig, Name: name, Value: kadm.StringPtr(value)})
	}

	_, err := admin.CreateTopic(ctx, spec.Partitions, replication, configs, spec.Name)
	switch {
	case err == nil:
		log.Info("created", "topic", spec.Name, "partitions", spec.Partitions, "configs", spec.Configs())
		return nil
	case errors.Is(err, kerr.TopicAlreadyExists):
		responses, err := admin.AlterTopicConfigs(ctx, alter, spec.Name)
		if err != nil {
			return err
		}
		for _, r := range responses {
			if r.Err != nil {
				return r.Err
			}
		}
		log.Info("exists, configs applied", "topic", spec.Name)
		return nil
	default:
		return err
	}
}
