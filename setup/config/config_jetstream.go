package config

import (
	"fmt"
	"log"
	"regexp"
)

type JetStream struct {
	Matrix *Global `yaml:"-"`

	// Persistent directory to store JetStream streams in.
	StoragePath Path `yaml:"storage_path"`
	// A list of NATS addresses to connect to. If none are specified, an
	// internal NATS server will be used when running in monolith mode only.
	Addresses []string `yaml:"addresses"`
	// The prefix to use for stream names for this homeserver - really only
	// useful if running more than one Dendrite on the same NATS deployment.
	TopicPrefix string `yaml:"topic_prefix"`
	// Keep all storage in memory. This is mostly useful for unit tests.
	InMemory bool `yaml:"-"`
}

func (c *JetStream) TopicFor(name string) string {
	return fmt.Sprintf("%s%s", c.TopicPrefix, name)
}

func (c *JetStream) Defaults() {
	c.Addresses = []string{}
	c.TopicPrefix = "Dendrite"
	reg, err := regexp.Compile(`[^a-zA-Z0-9\.]+`)
	if err != nil {
		log.Fatal(err)
	}
	c.StoragePath = Path("./" + reg.ReplaceAllString(string(c.Matrix.ServerName), ""))
}

func (c *JetStream) Verify(configErrs *ConfigErrors, isMonolith bool) {
	// If we are running in a polylith deployment then we need at least
	// one NATS JetStream server to talk to.
	if !isMonolith {
		checkNotZero(configErrs, "global.jetstream.addresses", int64(len(c.Addresses)))
	}
}