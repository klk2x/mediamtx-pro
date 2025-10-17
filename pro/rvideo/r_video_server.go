package rvideo

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/bluenviron/mediamtx/internal/logger"
)

type RVideoServer struct {
	clientListener     net.Listener
	connectionListener net.Listener
	clients            map[string]*RVideoClient
	rw                 *sync.RWMutex
	doneEndpoint       chan bool
	Version            string
	parent             logger.Writer
}

// Log implements logger.Writer.
func (p *RVideoServer) Log(level logger.Level, format string, args ...interface{}) {
	if p.parent != nil {
		p.parent.Log(level, "[rvideo] "+format, args...)
	}
}

func (p *RVideoServer) connHandle(conn net.Conn) (err error) {
	reader := bufio.NewReader(conn)
	var cmd string

	if cmd, err = reader.ReadString('\n'); err != nil {
		p.Log(logger.Error, "err=%s", err)
		return
	}

	var n int
	var mac string
	var client *RVideoClient

	if n, err = fmt.Sscanf(cmd, "R-VideoClient mac=%s\n", &mac); err == nil && n == 1 {
		client = NewRVideoClient(conn, mac)
		client.SetRVideoServer(p)
		if err = client.Serve(); err != nil {
			p.Log(logger.Error, "err=%s", err)
		}
		return
	}

	var url string
	var endpoint *RVideoEndpoint

	if n, err = fmt.Sscanf(cmd, "R-VideoEndpoint mac=%s url=%s\n", &mac, &url); err == nil && n == 2 {
		if client = p.GetClient(mac); client == nil {
			err = errors.New("not found client")
			p.Log(logger.Error, "err=%s", err)
			return
		}

		endpoint = NewRVideoEndpoint(conn, url)
		endpoint.SetRVideoClient(client)
		if err = endpoint.Serve(); err != nil {
			p.Log(logger.Error, "err=%s", err)
		}
		return
	}

	err = errors.New("command format err")
	p.Log(logger.Error, "err=%s", err)
	return
}

func (p *RVideoServer) Serve() (err error) {
	defer func() {
		err = p.clientListener.Close()
		if err != nil {
			p.Log(logger.Error, "err=%s", err)
		}
	}()

	for {
		var conn net.Conn
		conn, err = p.clientListener.Accept()
		if err != nil {
			p.Log(logger.Info, "Error accepting: %s", err.Error())
			continue
		}

		p.Log(logger.Info, "Accept: %s", conn.RemoteAddr().String())

		go func() {
			if err = p.connHandle(conn); err != nil {
				p.Log(logger.Error, "err=%s", err)
			}
		}()
	}
}

func (p *RVideoServer) AddClient(id string, cli *RVideoClient) {
	p.rw.Lock()
	defer p.rw.Unlock()

	p.clients[id] = cli
	p.Log(logger.Info, "Add R-Video Client [%d]: id=%s", len(p.clients), id)
}

func (p *RVideoServer) DelClient(id string, cli *RVideoClient) {
	p.rw.Lock()
	defer p.rw.Unlock()

	delete(p.clients, id)
	p.Log(logger.Info, "Del R-Video Client [%d]: id=%s, cli=%p", len(p.clients), id, cli)
}

func (p *RVideoServer) GetClient(id string) (cli *RVideoClient) {
	p.rw.RLock()
	defer p.rw.RUnlock()
	cli = p.clients[id]
	return
}

func NewRVideoServer(clientAddress string, parent logger.Writer) (rVideoServer *RVideoServer, err error) {
	rVideoServer = &RVideoServer{
		clients: make(map[string]*RVideoClient),
		rw:      new(sync.RWMutex),
		Version: "1.0.0",
		parent:  parent,
	}

	rVideoServer.clientListener, err = net.Listen("tcp", clientAddress)
	if err != nil {
		rVideoServer.Log(logger.Error, "err=%s", err)
		return nil, err
	}
	rVideoServer.Log(logger.Info, "R-Video Client listening on: %s", clientAddress)

	//connectionAddress := "0.0.0.0:1689"
	//server.connectionListener, err = net.Listen("tcp", connectionAddress)
	//if err != nil {
	//	log.Errorf("err=%s", err)
	//	return nil, err
	//}
	//log.Infof("R-Video Connection listening on: %s", connectionAddress)

	// Set global server instance
	server = rVideoServer

	go func() {
		err = rVideoServer.Serve()
	}()
	return rVideoServer, nil
}

var server *RVideoServer

func GetRVideoClientById(id string) (client *RVideoClient, err error) {

	// if server == nil {
	// 	if server, err = NewRVideoServer(); err != nil {
	// 		log.Errorf("err=%s", err)
	// 		return nil, err
	// 	}
	// 	go func() {
	// 		err = server.Serve()
	// 	}()
	// }

	if client = server.GetClient(id); client == nil {
		err = errors.New(fmt.Sprintf("no rvideo client: %s", id))
		server.Log(logger.Error, "err=%s", err)
		return nil, err
	}

	return client, nil
}
