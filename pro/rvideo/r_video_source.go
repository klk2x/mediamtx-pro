package rvideo

import (
	"fmt"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/headers"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/protocols/rtsp"
	"github.com/bluenviron/mediamtx/internal/protocols/tls"
)

type parent interface {
	logger.Writer
	SetReady(req defs.PathSourceStaticSetReadyReq) defs.PathSourceStaticSetReadyRes
	SetNotReady(req defs.PathSourceStaticSetNotReadyReq)
}

type Source struct {
	ReadTimeout    conf.Duration
	WriteTimeout   conf.Duration
	WriteQueueSize int
	Parent         parent
}

func createRangeHeader(cnf *conf.Path) (*headers.Range, error) {
	switch cnf.RTSPRangeType {
	case conf.RTSPRangeTypeClock:
		start, err := time.Parse("20060102T150405Z", cnf.RTSPRangeStart)
		if err != nil {
			return nil, err
		}

		return &headers.Range{
			Value: &headers.RangeUTC{
				Start: start,
			},
		}, nil

	case conf.RTSPRangeTypeNPT:
		start, err := time.ParseDuration(cnf.RTSPRangeStart)
		if err != nil {
			return nil, err
		}

		return &headers.Range{
			Value: &headers.RangeNPT{
				Start: start,
			},
		}, nil

	case conf.RTSPRangeTypeSMPTE:
		start, err := time.ParseDuration(cnf.RTSPRangeStart)
		if err != nil {
			return nil, err
		}

		return &headers.Range{
			Value: &headers.RangeSMPTE{
				Start: headers.RangeSMPTETime{
					Time: start,
				},
			},
		}, nil

	default:
		return nil, nil
	}
}

// Log implements StaticSource.
func (s *Source) Log(level logger.Level, format string, args ...interface{}) {
	s.Parent.Log(level, "[rvideo] "+format, args...)
}

// run implements sourceStaticImpl.
func (s *Source) Run(params defs.StaticSourceRunParams) (err error) {
	s.Log(logger.Debug, "connecting")

	var rvideoClient *RVideoClient
	var id string
	var n int

	if n, err = fmt.Sscanf(params.Conf.Source, "r-video://%s", &id); err != nil {
		s.Log(logger.Error, err.Error())
		return err
	}
	if n != 1 {
		s.Log(logger.Error, "source format err: %s", params.Conf.Source)
		return err
	}
	if rvideoClient, err = GetRVideoClientById(id); err != nil {
		return err
	}

	var conn *RVideoEndpoint

	if conn, err = rvideoClient.GetRVideoEndpointByUrl(params.ResolvedSource); err != nil {
		return err
	}

	u, err := base.ParseURL(params.ResolvedSource)
	if err != nil {
		return err
	}

	protocol := gortsplib.ProtocolTCP
	c := &gortsplib.Client{
		DialContext:    conn.DailRemote,
		Protocol:       &protocol,
		TLSConfig:      tls.MakeConfig(u.Hostname(), params.Conf.SourceFingerprint),
		ReadTimeout:    time.Duration(s.ReadTimeout),
		WriteTimeout:   time.Duration(s.WriteTimeout),
		WriteQueueSize: s.WriteQueueSize,
		AnyPortEnable:  params.Conf.RTSPAnyPort,
		OnRequest: func(req *base.Request) {
			s.Log(logger.Debug, "[c->s] %v", req)
		},
		OnResponse: func(res *base.Response) {
			s.Log(logger.Debug, "[s->c] %v", res)
		},
		OnTransportSwitch: func(err error) {
			s.Log(logger.Warn, err.Error())
		},
		OnPacketsLost: func(lost uint64) {
			s.Log(logger.Warn, "%d RTP packets lost", lost)
		},
		OnDecodeError: func(_ error) {
			// Just log, don't restart
		},
	}

	s.Log(logger.Info, "scheme=%s, host=%s", u.Scheme, u.Host)
	err = c.Start()
	if err != nil {
		return err
	}
	defer c.Close()

	readErr := make(chan error)
	go func() {
		readErr <- func() error {
			desc, _, err := c.Describe(u)
			if err != nil {
				return err
			}

			err = c.SetupAll(desc.BaseURL, desc.Medias)
			if err != nil {
				return err
			}

			res := s.Parent.SetReady(defs.PathSourceStaticSetReadyReq{
				Desc:               desc,
				GenerateRTPPackets: false,
				FillNTP:            true,
			})
			if res.Err != nil {
				return res.Err
			}

			defer s.Parent.SetNotReady(defs.PathSourceStaticSetNotReadyReq{})

			rtsp.ToStream(
				c,
				desc.Medias,
				params.Conf,
				res.Stream,
				s)

			rangeHeader, err := createRangeHeader(params.Conf)
			if err != nil {
				return err
			}

			_, err = c.Play(rangeHeader)
			if err != nil {
				return err
			}

			return c.Wait()
		}()
	}()

	for {
		select {
		case err := <-readErr:
			return err

		case <-params.ReloadConf:

		case <-params.Context.Done():
			c.Close()
			<-readErr
			return nil
		}
	}
}

// APISourceDescribe implements StaticSource.
func (*Source) APISourceDescribe() defs.APIPathSourceOrReader {
	return defs.APIPathSourceOrReader{
		Type: "rvideoSource",
		ID:   "",
	}
}
