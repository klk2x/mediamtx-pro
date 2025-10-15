package websocketapi

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	HandshakeTimeout: time.Second * 10,
	ReadBufferSize:   1024,
	WriteBufferSize:  1024,
	// 解决跨域问题
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

var (
	// 消息通道
	news = make(map[string]chan interface{})
	// websocket客户端链接池
	Clients = make(map[string]*Client)
	// 互斥锁，防止程序对统一资源同时进行读写
	mux sync.RWMutex
)

// Client 结构体，包含连接和发送消息的通道
type Client struct {
	conn   *websocket.Conn
	sendCh chan interface{}
	mu     sync.Mutex // 保护该连接的写操作
}

func WebSocketHandler(c *gin.Context) {
	var conn *websocket.Conn
	var err error
	id := uuid.New().String()

	// 创建一个定时器用于服务端心跳
	pingTicker := time.NewTicker(time.Second * 10)
	defer pingTicker.Stop() // 确保退出时停止定时器

	// Upgrade the connection
	conn, err = upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Println(logger.Error, err.Error())
		return
	}

	// 创建客户端对象
	client := &Client{
		conn:   conn,
		sendCh: make(chan interface{}),
	}

	// 把客户端对象添加到连接池
	addClient(id, client)

	// 启动一个Goroutine处理写操作
	go client.writePump()

	// 启动心跳机制
	for {
		select {
		case <-pingTicker.C:
			if err := sendPing(client); err != nil {
				log.Println(err)
				deleteClient(id)
				return
			}
		}
	}
}

// writePump 处理单个客户端的所有 WebSocket 写操作
func (c *Client) writePump() {
	for msg := range c.sendCh {
		c.mu.Lock() // 确保单一 goroutine 进行写操作
		err := c.conn.WriteJSON(msg)
		c.mu.Unlock()

		if err != nil {
			log.Println("write error:", err)
			c.conn.Close()
			break
		}
	}
}

// Helper function to send a ping message
func sendPing(client *Client) error {
	client.mu.Lock() // 使用互斥锁保护写操作
	defer client.mu.Unlock()
	client.conn.SetWriteDeadline(time.Now().Add(time.Second * 20))
	return client.conn.WriteMessage(websocket.PingMessage, []byte{})
}

// 将客户端添加到连接池
func addClient(id string, client *Client) {
	mux.Lock()
	defer mux.Unlock()
	Clients[id] = client
}

// 获取指定客户端
func getClient(id string) (*Client, bool) {
	mux.RLock()
	defer mux.RUnlock()
	client, exist := Clients[id]
	return client, exist
}

// 删除客户端并关闭连接
func deleteClient(id string) {
	mux.Lock()
	defer mux.Unlock()
	if client, ok := Clients[id]; ok {
		close(client.sendCh) // 关闭发送通道以停止 writePump
		client.conn.Close()
		delete(Clients, id)
		log.Println(id + " websocket exit")
	}
}

// 将消息发送到所有客户端
func PostMessage(data interface{}) {
	mux.RLock() // 读取锁保护 Clients
	for _, client := range Clients {
		select {
		case client.sendCh <- data:
		default:
			log.Println("sendCh full")
			// deleteClient(id)
		}
	}
	mux.RUnlock() // 释放读取锁
}
