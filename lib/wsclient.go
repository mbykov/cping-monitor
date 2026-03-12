package lib

import (
	"crypto/tls"
	"fmt"
	"math"
	"net/url"
	"sync"
	"time"

    "github.com/gorilla/websocket"
)

type WSClient struct {
	conn      *websocket.Conn
	url       string
	cert, key string
	useHTTPS  bool
	connected bool
	mu        sync.Mutex // Для потокобезопасной записи
}

func NewWSClient(host string, port int, cert, key string, useHTTPS bool) *WSClient {
	scheme := "ws"
	if useHTTPS { scheme = "wss" }
	u := url.URL{Scheme: scheme, Host: fmt.Sprintf("%s:%d", host, port), Path: "/"}
	return &WSClient{url: u.String(), cert: cert, key: key, useHTTPS: useHTTPS}
}

func (c *WSClient) Connect() error {
	dialer := websocket.DefaultDialer
	if c.useHTTPS && c.cert != "" {
		cert, err := tls.LoadX509KeyPair(c.cert, c.key)
		if err != nil { return err }
		dialer.TLSClientConfig = &tls.Config{Certificates: []tls.Certificate{cert}, InsecureSkipVerify: true}
	}
	conn, _, err := dialer.Dial(c.url, nil)
	if err != nil { return err }
	c.conn = conn
	c.connected = true
	return nil
}

func (c *WSClient) ReadRawMessage() (map[string]interface{}, error) {
    var msg map[string]interface{}
    err := c.conn.ReadJSON(&msg)
    return msg, err
}

func (c *WSClient) SendAudioFloat32(chunk []float32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected || c.conn == nil { return fmt.Errorf("not connected") }

	buf := make([]byte, len(chunk)*4)
	for i, sample := range chunk {
		bits := math.Float32bits(sample)
		buf[i*4] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return c.conn.WriteMessage(websocket.BinaryMessage, buf)
}

func (c *WSClient) SendBuffer(samples []float32, chunkMs int, sampleRate int) {
	go func() {
		chunkSize := sampleRate * chunkMs / 1000
		for i := 0; i < len(samples); i += chunkSize {
			end := i + chunkSize
			if end > len(samples) { end = len(samples) }
			_ = c.SendAudioFloat32(samples[i:end])
			time.Sleep(time.Duration(chunkMs) * time.Millisecond)
		}
	}()
}
