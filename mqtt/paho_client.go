package mqtt

import (
	"fmt"
	"time"

	"moonraker2mqtt/logger"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type MQTTClient interface {
	Connect() error
	Disconnect() error
	IsConnected() bool
	Publish(topic string, payload []byte, qos byte, retain bool, maxRetries int) error
	Subscribe(topic string, handler MessageHandler) error
	Unsubscribe(topic string) error
}

type MessageHandler func(topic string, payload []byte)

type PahoClient struct {
	host        string
	port        int
	clientID    string
	username    string
	password    string
	useTLS      bool
	client      mqtt.Client
	logger      logger.Logger
	subscribers map[string]MessageHandler
}

func NewPahoClient(host string, port int, clientID, username, password string, useTLS bool, logger logger.Logger) *PahoClient {
	return &PahoClient{
		host:        host,
		port:        port,
		clientID:    clientID,
		username:    username,
		password:    password,
		useTLS:      useTLS,
		logger:      logger,
		subscribers: make(map[string]MessageHandler),
	}
}

func (c *PahoClient) Connect() error {
	opts := mqtt.NewClientOptions()
	scheme := "tcp"
	if c.useTLS {
		scheme = "tls"
	}

	brokerURL := fmt.Sprintf("%s://%s:%d", scheme, c.host, c.port)
	opts.AddBroker(brokerURL)
	opts.SetClientID(c.clientID)

	if c.username != "" {
		opts.SetUsername(c.username)
	}

	if c.password != "" {
		opts.SetPassword(c.password)
	}

	opts.SetKeepAlive(60 * time.Second)
	opts.SetDefaultPublishHandler(c.defaultMessageHandler)
	opts.SetPingTimeout(30 * time.Second)
	opts.SetConnectTimeout(30 * time.Second)
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(10 * time.Second)
	opts.SetConnectionLostHandler(c.connectionLostHandler)
	opts.SetOnConnectHandler(c.onConnectHandler)
	opts.SetReconnectingHandler(c.reconnectingHandler)

	c.client = mqtt.NewClient(opts)

	c.logger.Info("Connecting to MQTT broker at %s", brokerURL)

	if token := c.client.Connect(); token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to connect to MQTT broker: %w", token.Error())
	}

	c.logger.Info("Successfully connected to MQTT broker")
	return nil
}

func (c *PahoClient) Disconnect() error {
	if c.client != nil && c.client.IsConnected() {
		c.logger.Info("Disconnecting from MQTT broker")
		c.client.Disconnect(250)
	}
	return nil
}

func (c *PahoClient) IsConnected() bool {
	if c.client == nil {
		return false
	}
	return c.client.IsConnected()
}

func (c *PahoClient) Publish(topic string, payload []byte, qos byte, retain bool, maxRetries int) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to MQTT broker")
	}

	token := c.client.Publish(topic, qos, retain, payload)
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to publish message: %w", token.Error())
	}

	return nil
}

func (c *PahoClient) Subscribe(topic string, handler MessageHandler) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to MQTT broker")
	}

	c.subscribers[topic] = handler

	token := c.client.Subscribe(topic, 0, func(client mqtt.Client, msg mqtt.Message) {
		if handler, exists := c.subscribers[msg.Topic()]; exists {
			handler(msg.Topic(), msg.Payload())
		}
	})

	if token.Wait() && token.Error() != nil {
		delete(c.subscribers, topic)
		return fmt.Errorf("failed to subscribe to topic %s: %w", topic, token.Error())
	}

	c.logger.Info("Successfully subscribed to topic: %s", topic)
	return nil
}

func (c *PahoClient) Unsubscribe(topic string) error {
	if !c.IsConnected() {
		return fmt.Errorf("not connected to MQTT broker")
	}

	token := c.client.Unsubscribe(topic)
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to unsubscribe from topic %s: %w", topic, token.Error())
	}

	delete(c.subscribers, topic)
	c.logger.Info("Successfully unsubscribed from topic: %s", topic)
	return nil
}

func (c *PahoClient) defaultMessageHandler(client mqtt.Client, msg mqtt.Message) {
	c.logger.Debug("Received message on topic %s: %s", msg.Topic(), string(msg.Payload()))
}

func (c *PahoClient) connectionLostHandler(client mqtt.Client, err error) {
	c.logger.Warn("MQTT connection lost: %v", err)
}

func (c *PahoClient) onConnectHandler(client mqtt.Client) {
	c.logger.Info("MQTT connection established")

	for topic, handler := range c.subscribers {
		c.logger.Info("Resubscribing to topic: %s", topic)
		token := client.Subscribe(topic, 0, func(client mqtt.Client, msg mqtt.Message) {
			handler(msg.Topic(), msg.Payload())
		})
		if token.Wait() && token.Error() != nil {
			c.logger.Error("Failed to resubscribe to topic %s: %v", topic, token.Error())
		}
	}
}

func (c *PahoClient) reconnectingHandler(client mqtt.Client, opts *mqtt.ClientOptions) {
	c.logger.Info("Attempting to reconnect to MQTT broker...")
}
