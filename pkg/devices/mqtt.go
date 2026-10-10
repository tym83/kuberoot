package devices

import (
	"fmt"
	"os"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Brokers keeps one connection to each MQTT broker the routes name,
// connecting again by itself when one drops.
type Brokers struct {
	mu      sync.Mutex
	clients map[string]mqtt.Client
}

func (b *Brokers) client(broker string) mqtt.Client {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.clients == nil {
		b.clients = map[string]mqtt.Client{}
	}
	if c, ok := b.clients[broker]; ok {
		return c
	}
	host, _ := os.Hostname()
	opts := mqtt.NewClientOptions().AddBroker(broker).SetClientID("kuberoot-devices-" + host).
		SetAutoReconnect(true).SetConnectRetry(true).SetConnectRetryInterval(2 * time.Second).
		SetConnectTimeout(3 * time.Second).SetMaxReconnectInterval(10 * time.Second)
	c := mqtt.NewClient(opts)
	c.Connect() // in the background: it keeps retrying
	b.clients[broker] = c
	return c
}

// Connected reports whether the broker takes messages now.
func (b *Brokers) Connected(broker string) bool {
	return b.client(broker).IsConnectionOpen()
}

// Publish sends a message, waiting a few seconds for the broker to take it.
func (b *Brokers) Publish(broker, topic string, qos byte, retain bool, payload []byte) error {
	c := b.client(broker)
	if !c.IsConnectionOpen() {
		return fmt.Errorf("broker %s is not connected", broker)
	}
	t := c.Publish(topic, qos, retain, payload)
	if !t.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("broker %s did not take the message in time", broker)
	}
	return t.Error()
}

// Keep drops the connections to brokers no route names any more.
func (b *Brokers) Keep(brokers map[string]bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for url, c := range b.clients {
		if !brokers[url] {
			c.Disconnect(250)
			delete(b.clients, url)
		}
	}
}
