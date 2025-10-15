package rvideo

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/logger"
)

type RVideoClient struct {
	conn      net.Conn
	server    *RVideoServer
	id        string
	endpoints map[string]*RVideoEndpoint
	rw        *sync.RWMutex
	done      chan bool
}

func NewRVideoClient(conn net.Conn, id string) *RVideoClient {
	return &RVideoClient{
		conn:      conn,
		id:        id,
		endpoints: make(map[string]*RVideoEndpoint),
		rw:        new(sync.RWMutex),
	}
}

func (p *RVideoClient) SetRVideoServer(server *RVideoServer) {
	if server == nil {
		// Cannot log without server
		return
	}

	p.server = server
}

func (p *RVideoClient) Read(b []byte) (n int, err error) {
	if n, err = p.conn.Read(b); err != nil {
		if p.server != nil {
			p.server.Log(logger.Error, "err=%s", err)
		}
		if p.server != nil {
			p.server.DelClient(p.id, p)
		}
		return
	}

	if p.server != nil {
		p.server.Log(logger.Debug, "[CLIENT] <<< [IN]: [%d][%s]", n, string(b[:n]))
	}

	return
}

func (p *RVideoClient) Write(b []byte) (n int, err error) {
	if n, err = p.conn.Write(b); err != nil {
		if p.server != nil {
			p.server.Log(logger.Error, "err=%s", err)
			p.server.DelClient(p.id, p)
		}
		return
	}

	if p.server != nil {
		p.server.Log(logger.Debug, "[CLIENT] >>> [OUT]: [%d][%s]", n, string(b[:n]))
	}
	return
}

func (p *RVideoClient) handleConn() (err error) {
	reader := bufio.NewReader(p)
	for {
		if _, err = reader.ReadString('\n'); err != nil {
			if p.server != nil {
				p.server.Log(logger.Error, "err=%s", err)
			}
			return
		}
	}
}

func (p *RVideoClient) Serve() (err error) {
	p.server.AddClient(p.id, p)
	go func() {
		if err = p.handleConn(); err != nil {
			return
		}
	}()
	return nil
}

func (p *RVideoClient) AddEndpoint(url string, ep *RVideoEndpoint) {
	p.rw.Lock()
	defer p.rw.Unlock()

	p.endpoints[url] = ep
	if p.server != nil {
		p.server.Log(logger.Info, "Add R-Video Endpoint [%d]: url=%s", len(p.endpoints), url)
	}
	if p.done != nil {
		p.done <- true
	}
}

func (p *RVideoClient) DelEndpoint(url string, ep *RVideoEndpoint) {
	p.rw.Lock()
	defer p.rw.Unlock()

	delete(p.endpoints, url)
	if p.server != nil {
		p.server.Log(logger.Info, "Del R-Video Endpoint [%d]: url=%s, ep=%p", len(p.endpoints), url, ep)
	}
}

func (p *RVideoClient) GetEndpoint(url string) (ep *RVideoEndpoint) {
	p.rw.RLock()
	defer p.rw.RUnlock()
	ep = p.endpoints[url]
	return
}

func (p *RVideoClient) WaitRVideoEndpoint(timeout time.Duration) (err error) {
	if p.done != nil {
		err = errors.New("in waiting process")
		if p.server != nil {
			p.server.Log(logger.Error, "err=%s", err)
		}
		return
	}

	p.done = make(chan bool)
	ticker := time.NewTicker(timeout)

	select {
	case <-p.done:
		err = nil
	case <-ticker.C:
		err = errors.New("wait for endpoint timeout")
		if p.server != nil {
			p.server.Log(logger.Error, "err=%s", err)
		}
	}

	p.done = nil

	return
}

func (p *RVideoClient) GetRVideoEndpointByUrl(url string) (ep *RVideoEndpoint, err error) {
	if ep = p.GetEndpoint(url); ep != nil {
		return ep, nil
	}

	cmd := fmt.Sprintf("R-VideoClient url=%s\n", url)
	if _, err = p.Write([]byte(cmd)); err != nil {
		if p.server != nil {
			p.server.Log(logger.Error, "err=%s", err)
		}
		return
	}

	if err = p.WaitRVideoEndpoint(5 * time.Second); err != nil {
		return
	}

	if ep = p.GetEndpoint(url); ep == nil {
		err = errors.New(fmt.Sprintf("not found: url=%s, client=%s", url, p.id))
		if p.server != nil {
			p.server.Log(logger.Error, "err=%s", err)
		}
		return
	}

	return ep, nil
}
