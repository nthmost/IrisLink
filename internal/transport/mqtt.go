package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/google/uuid"

	ilcrypto "github.com/nthmost/IrisLink/internal/crypto"
)

const (
	topicMessages = "irislink/%s/messages"
	topicPresence = "irislink/%s/presence"
	topicControl  = "irislink/%s/control"
)

// ContextBlock carries a file excerpt attached to an outgoing envelope.
type ContextBlock struct {
	Source  string `json:"source"`
	Content string `json:"content"`
}

// Envelope is the cleartext payload that gets encrypted on the wire.
type Envelope struct {
	Sender    string         `json:"sender"`
	Text      string         `json:"text"`
	Timestamp int64          `json:"timestamp"`
	Type      string         `json:"type"` // "message", "presence", "control"
	Context   []ContextBlock `json:"context,omitempty"`
}

// Client is an IrisLink MQTT session for one room.
type Client struct {
	paho   *paho.Client
	roomID string
	key    [32]byte
	handle string
	onMsg  func(Envelope)
}

// Connect establishes an MQTT v5 connection to brokerAddr and subscribes to the room.
// username and password may be empty for anonymous brokers.
func Connect(ctx context.Context, brokerAddr, roomID, handle string, key [32]byte, onMsg func(Envelope), opts ...string) (*Client, error) {
	conn, err := net.DialTimeout("tcp", brokerAddr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("cannot reach broker at %s: %w", brokerAddr, err)
	}

	c := &Client{roomID: roomID, key: key, handle: handle, onMsg: onMsg}

	router := paho.NewStandardRouter()
	router.RegisterHandler(fmt.Sprintf(topicMessages, roomID), c.handleMessage)
	router.RegisterHandler(fmt.Sprintf(topicPresence, roomID), c.handleMessage)
	router.RegisterHandler(fmt.Sprintf(topicControl, roomID), c.handleMessage)

	pc := paho.NewClient(paho.ClientConfig{
		Conn:   conn,
		Router: router,
	})
	c.paho = pc

	cp := &paho.Connect{
		KeepAlive:  30,
		ClientID:   uuid.New().String(), // random, never OTP-derived
		CleanStart: true,
	}
	if len(opts) >= 1 && opts[0] != "" {
		cp.Username = opts[0]
		cp.UsernameFlag = true
	}
	if len(opts) >= 2 && opts[1] != "" {
		cp.Password = []byte(opts[1])
		cp.PasswordFlag = true
	}

	ca, err := pc.Connect(ctx, cp)
	if err != nil {
		return nil, fmt.Errorf("MQTT connect failed: %w", err)
	}
	if ca.ReasonCode != 0 {
		return nil, fmt.Errorf("MQTT connect rejected: reason %d", ca.ReasonCode)
	}

	subs := []paho.SubscribeOptions{
		{Topic: fmt.Sprintf(topicMessages, roomID), QoS: 1},
		{Topic: fmt.Sprintf(topicPresence, roomID), QoS: 1},
		{Topic: fmt.Sprintf(topicControl, roomID), QoS: 1},
	}
	if _, err := pc.Subscribe(ctx, &paho.Subscribe{Subscriptions: subs}); err != nil {
		return nil, fmt.Errorf("MQTT subscribe failed: %w", err)
	}

	return c, nil
}

// Publish sends an encrypted envelope to the messages topic.
func (c *Client) Publish(ctx context.Context, env Envelope) error {
	env.Timestamp = time.Now().UnixMilli()
	env.Sender = c.handle
	plain, err := json.Marshal(env)
	if err != nil {
		return err
	}
	wire, err := ilcrypto.Seal(plain, c.key)
	if err != nil {
		return err
	}
	topic := fmt.Sprintf(topicMessages, c.roomID)
	if env.Type == "presence" {
		topic = fmt.Sprintf(topicPresence, c.roomID)
	} else if env.Type == "control" {
		topic = fmt.Sprintf(topicControl, c.roomID)
	}
	_, err = c.paho.Publish(ctx, &paho.Publish{
		QoS:     1,
		Topic:   topic,
		Payload: wire,
	})
	return err
}

// Disconnect cleanly closes the MQTT connection.
func (c *Client) Disconnect(ctx context.Context) {
	c.paho.Disconnect(&paho.Disconnect{ReasonCode: 0})
}

func (c *Client) handleMessage(pub *paho.Publish) {
	plain, err := ilcrypto.Open(pub.Payload, c.key)
	if err != nil {
		return // silently drop undecryptable messages
	}
	var env Envelope
	if err := json.Unmarshal(plain, &env); err != nil {
		return
	}
	if env.Sender == c.handle {
		return // skip own messages
	}
	c.onMsg(env)
}

// Topics returns the three topic strings for this room (for display/debug).
func Topics(roomID string) (messages, presence, control string) {
	return fmt.Sprintf(topicMessages, roomID),
		fmt.Sprintf(topicPresence, roomID),
		fmt.Sprintf(topicControl, roomID)
}
