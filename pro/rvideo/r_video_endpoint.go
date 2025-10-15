package rvideo

import (
	"context"
	"net"
	"strings"

	"github.com/bluenviron/mediamtx/internal/logger"
)

type RVideoEndpoint struct {
	net.Conn
	client *RVideoClient
	url    string
}

func (p *RVideoEndpoint) Read(b []byte) (n int, err error) {
	if n, err = p.Conn.Read(b); err != nil {
		if p.client != nil && p.client.server != nil {
			p.client.server.Log(logger.Error, "err=%s", err)
		}
		_ = p.Conn.Close()
		if p.client != nil {
			p.client.DelEndpoint(p.url, p)
		}
		return
	}

	if p.client != nil && p.client.server != nil {
		p.client.server.Log(logger.Debug, "[REMOTE] <<< [IN]: [%d]", n)
		if strings.HasPrefix(string(b), "RTSP") {
			p.client.server.Log(logger.Debug, "[REMOTE] <<< [IN]: [%s]", string(b[:n]))
		}
	}

	return
}

func (p *RVideoEndpoint) Write(b []byte) (n int, err error) {
	if n, err = p.Conn.Write(b); err != nil {
		if p.client != nil && p.client.server != nil {
			p.client.server.Log(logger.Error, "err=%s", err)
		}
		_ = p.Conn.Close()
		if p.client != nil {
			p.client.DelEndpoint(p.url, p)
		}
		return
	}

	if p.client != nil && p.client.server != nil {
		p.client.server.Log(logger.Debug, "[REMOTE] >>> [OUT]: [%d]", n)
		if strings.HasPrefix(string(b), "RTSP") {
			p.client.server.Log(logger.Debug, "[REMOTE] >>> [OUT]: [%s]", string(b))
		}
	}
	return
}

func (p *RVideoEndpoint) DailRemote(ctx context.Context, network, address string) (c net.Conn, err error) {
	if p.client != nil && p.client.server != nil {
		p.client.server.Log(logger.Info, "DailRemote: address=%s", address)
	}
	return p, nil
}

func (p *RVideoEndpoint) SetRVideoClient(cli *RVideoClient) {
	p.client = cli
}

func (p *RVideoEndpoint) Serve() (err error) {
	p.client.AddEndpoint(p.url, p)
	return nil
}

func NewRVideoEndpoint(conn net.Conn, url string) (e *RVideoEndpoint) {
	return &RVideoEndpoint{
		Conn: conn,
		url:  url,
	}
}
