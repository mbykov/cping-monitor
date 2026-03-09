package lib

import (
    "crypto/tls"
    // "encoding/json"
    "fmt"
    "log"
    "math"
    "net/url"
    "sync"
    "time"

    "github.com/gorilla/websocket"
)

type WSClient struct {
    conn          *websocket.Conn
    url           string
    cert          string
    key           string
    useHTTPS      bool
    connected     bool
    mu            sync.RWMutex
    responseTimes []time.Duration
}

func NewWSClient(host string, port int, cert, key string, useHTTPS bool) *WSClient {
    scheme := "ws"
    if useHTTPS {
        scheme = "wss"
    }
    u := url.URL{Scheme: scheme, Host: fmt.Sprintf("%s:%d", host, port), Path: "/"}

    log.Printf("Creating WebSocket client for %s", u.String())

    return &WSClient{
        url:      u.String(),
        cert:     cert,
        key:      key,
        useHTTPS: useHTTPS,
    }
}

func (c *WSClient) Connect() error {
    c.mu.Lock()
    defer c.mu.Unlock()

    dialer := websocket.DefaultDialer

    if c.useHTTPS && c.cert != "" && c.key != "" {
        cert, err := tls.LoadX509KeyPair(c.cert, c.key)
        if err != nil {
            return fmt.Errorf("failed to load cert: %w", err)
        }
        dialer.TLSClientConfig = &tls.Config{
            Certificates:       []tls.Certificate{cert},
            InsecureSkipVerify: true,
        }
    }

    log.Printf("Dialing %s", c.url)
    conn, _, err := dialer.Dial(c.url, nil)
    if err != nil {
        return fmt.Errorf("dial failed: %w", err)
    }

    c.conn = conn
    c.connected = true

    log.Println("WebSocket connected successfully")
    return nil
}

// ReadMessage читает одно сообщение и возвращает тип и текст
func (c *WSClient) ReadMessage() (string, string, error) {
    c.mu.RLock()
    conn := c.conn
    c.mu.RUnlock()

    if conn == nil {
        return "", "", fmt.Errorf("not connected")
    }

    var msg struct {
        Type string `json:"type"`
        Text string `json:"text"`
    }

    err := conn.ReadJSON(&msg)
    if err != nil {
        return "", "", err
    }

    return msg.Type, msg.Text, nil
}

func (c *WSClient) SendAudioFloat32(chunk []float32) error {
    c.mu.RLock()
    conn := c.conn
    connected := c.connected
    c.mu.RUnlock()

    if !connected || conn == nil {
        return fmt.Errorf("not connected")
    }

    start := time.Now()

    buf := make([]byte, len(chunk)*4)
    for i, sample := range chunk {
        bits := math.Float32bits(sample)
        buf[i*4] = byte(bits)
        buf[i*4+1] = byte(bits >> 8)
        buf[i*4+2] = byte(bits >> 16)
        buf[i*4+3] = byte(bits >> 24)
    }

    err := conn.WriteMessage(websocket.BinaryMessage, buf)
    if err == nil {
        c.mu.Lock()
        c.responseTimes = append(c.responseTimes, time.Since(start))
        c.mu.Unlock()
    }
    return err
}

func (c *WSClient) Close() error {
    c.mu.Lock()
    defer c.mu.Unlock()

    if !c.connected {
        return nil
    }

    c.connected = false
    if c.conn != nil {
        err := c.conn.Close()
        c.conn = nil
        return err
    }
    return nil
}

func (c *WSClient) IsConnected() bool {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.connected
}
