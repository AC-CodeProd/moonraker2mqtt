package mqtt

type MQTTClient interface {
	Connect() error
	Disconnect() error
	IsConnected() bool
	Publish(topic string, payload []byte, qos byte, retain bool, maxRetries int) error
	Subscribe(topic string, handler MessageHandler) error
	Unsubscribe(topic string) error
}

type MessageHandler func(topic string, payload []byte)
