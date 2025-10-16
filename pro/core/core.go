// Package core contains the Pro core implementation.
package core

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/gin-gonic/gin"
	livekitauth "github.com/livekit/protocol/auth"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/confwatcher"
	"github.com/bluenviron/mediamtx/internal/externalcmd"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/metrics"
	"github.com/bluenviron/mediamtx/internal/rlimit"
	"github.com/bluenviron/mediamtx/internal/servers/rtmp"
	"github.com/bluenviron/mediamtx/internal/servers/rtsp"
	"github.com/bluenviron/mediamtx/internal/servers/webrtc"

	proapi "github.com/bluenviron/mediamtx/pro/api"
	"github.com/bluenviron/mediamtx/pro/healthcheck"
	"github.com/bluenviron/mediamtx/pro/recorder"
	prorecordcleaner "github.com/bluenviron/mediamtx/pro/recordcleaner"
	"github.com/bluenviron/mediamtx/pro/rvideo"
)

// Version is the Pro version.
const Version = "v1.0.0-pro"

var started = time.Now()

var defaultConfPaths = []string{
	"config.yml",
	"mediamtx.yml",
}

var defaultConfPathsNotWin = []string{
	"/usr/local/etc/config.yml",
	"/usr/local/etc/mediamtx.yml",
	"/usr/etc/config.yml",
	"/usr/etc/mediamtx.yml",
	"/etc/mediamtx/config.yml",
	"/etc/mediamtx/mediamtx.yml",
}

func getRTPMaxPayloadSize(udpMaxPayloadSize int, rtspEncryption conf.Encryption) int {
	v := udpMaxPayloadSize - 12
	if rtspEncryption == conf.EncryptionOptional || rtspEncryption == conf.EncryptionStrict {
		v -= 10
	}
	return v
}

func atLeastOneRecordClearDaysAgo(pathConfs map[string]*conf.Path) bool {
	for _, e := range pathConfs {
		if e.RecordClearDaysAgo > 0 {
			return true
		}
	}
	return false
}

// Core is the Pro version core.
type Core struct {
	ctx             context.Context
	ctxCancel       func()
	confPath        string
	conf            *conf.Conf
	logger          *logger.Logger
	externalCmdPool *externalcmd.Pool
	authManager     *auth.Manager
	metrics         *metrics.Metrics
	recordCleaner   *prorecordcleaner.Cleaner
	pathManager     *pathManager
	rtspServer      *rtsp.Server
	rtspsServer     *rtsp.Server
	rtmpServer      *rtmp.Server
	rtmpsServer     *rtmp.Server
	webRTCServer    *webrtc.Server
	rvideoServer    *rvideo.RVideoServer
	recordManager   *recorder.Manager
	api             *proapi.APIV2
	authMiddleware  *proapi.APIKeyAuthMiddleware
	healthChecker   *healthcheck.Checker
	confWatcher     *confwatcher.ConfWatcher

	// channels
	chAPIConfigSet chan *conf.Conf

	// done
	done chan struct{}
}

// New allocates a Pro Core.
func New(args []string) (*Core, bool) {
	ctx, ctxCancel := context.WithCancel(context.Background())

	confPath := ""
	if len(args) > 0 {
		confPath = args[0]
	}

	p := &Core{
		ctx:            ctx,
		ctxCancel:      ctxCancel,
		chAPIConfigSet: make(chan *conf.Conf),
		done:           make(chan struct{}),
	}

	tempLogger, _ := logger.New(logger.Warn, []logger.Destination{logger.DestinationStdout}, "", "")

	confPaths := append([]string(nil), defaultConfPaths...)
	if runtime.GOOS != "windows" {
		confPaths = append(confPaths, defaultConfPathsNotWin...)
	}

	var err error
	p.conf, p.confPath, err = conf.Load(confPath, confPaths, tempLogger)
	if err != nil {
		fmt.Printf("ERR: %s\n", err)
		ctxCancel()
		return nil, false
	}

	err = p.createResources(true)
	if err != nil {
		if p.logger != nil {
			p.Log(logger.Error, "%s", err)
		} else {
			fmt.Printf("ERR: %s\n", err)
		}
		p.closeResources(nil, false)
		ctxCancel()
		return nil, false
	}

	go p.run()

	return p, true
}

// Log implements logger.Writer.
func (p *Core) Log(level logger.Level, format string, args ...interface{}) {
	p.logger.Log(level, format, args...)
}

func (p *Core) run() {
	defer close(p.done)

	confChanged := func() chan struct{} {
		if p.confWatcher != nil {
			return p.confWatcher.Watch()
		}
		return make(chan struct{})
	}()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	if runtime.GOOS == "linux" {
		signal.Notify(interrupt, syscall.SIGTERM)
	}

outer:
	for {
		select {
		case <-confChanged:
			p.Log(logger.Info, "reloading configuration (file changed)")

			newConf, _, err := conf.Load(p.confPath, nil, p.logger)
			if err != nil {
				p.Log(logger.Error, "%s", err)
				break outer
			}

			err = p.reloadConf(newConf, false)
			if err != nil {
				p.Log(logger.Error, "%s", err)
				break outer
			}

		case newConf := <-p.chAPIConfigSet:
			p.Log(logger.Info, "reloading configuration (API request)")

			err := p.reloadConf(newConf, true)
			if err != nil {
				p.Log(logger.Error, "%s", err)
				break outer
			}

		case <-interrupt:
			p.Log(logger.Info, "shutting down gracefully")
			break outer

		case <-p.ctx.Done():
			break outer
		}
	}

	p.ctxCancel()
	p.closeResources(nil, false)
}

func (p *Core) createResources(initial bool) error {
	var err error

	if p.logger == nil {
		p.logger, err = logger.New(
			logger.Level(p.conf.LogLevel),
			p.conf.LogDestinations,
			p.conf.LogFile,
			p.conf.SysLogPrefix,
		)
		if err != nil {
			return err
		}
	}

	if initial {
		p.Log(logger.Info, "MediaMTX Pro %s", Version)

		if p.confPath != "" {
			a, _ := filepath.Abs(p.confPath)
			p.Log(logger.Info, "configuration loaded from %s", a)
		} else {
			list := make([]string, len(defaultConfPaths))
			for i, pa := range defaultConfPaths {
				a, _ := filepath.Abs(pa)
				list[i] = a
			}
			p.Log(logger.Warn,
				"configuration file not found (looked in %s), using an empty configuration",
				strings.Join(list, ", "))
		}

		// 验证许可证密钥（不检查过期时间）
		p.ValidateKey(false)

		rlimit.Raise() //nolint:errcheck
		gin.SetMode(gin.ReleaseMode)

		p.externalCmdPool = &externalcmd.Pool{}
		p.externalCmdPool.Initialize()
	}

	if p.authManager == nil {
		p.authManager = &auth.Manager{
			Method:             p.conf.AuthMethod,
			InternalUsers:      p.conf.AuthInternalUsers,
			HTTPAddress:        p.conf.AuthHTTPAddress,
			HTTPExclude:        p.conf.AuthHTTPExclude,
			JWTJWKS:            p.conf.AuthJWTJWKS,
			JWTJWKSFingerprint: p.conf.AuthJWTJWKSFingerprint,
			JWTClaimKey:        p.conf.AuthJWTClaimKey,
			JWTExclude:         p.conf.AuthJWTExclude,
			JWTInHTTPQuery:     p.conf.AuthJWTInHTTPQuery,
			ReadTimeout:        time.Duration(p.conf.ReadTimeout),
		}
	}

	if p.conf.Metrics && p.metrics == nil {
		i := &metrics.Metrics{
			Address:        p.conf.MetricsAddress,
			Encryption:     p.conf.MetricsEncryption,
			ServerKey:      p.conf.MetricsServerKey,
			ServerCert:     p.conf.MetricsServerCert,
			AllowOrigin:    p.conf.MetricsAllowOrigin,
			TrustedProxies: p.conf.MetricsTrustedProxies,
			ReadTimeout:    p.conf.ReadTimeout,
			WriteTimeout:   p.conf.WriteTimeout,
			AuthManager:    p.authManager,
			Parent:         p,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.metrics = i
	}

	// Pro Record Cleaner: Use date-based folder cleanup
	if p.recordCleaner == nil &&
		atLeastOneRecordClearDaysAgo(p.conf.Paths) {
		p.recordCleaner = &prorecordcleaner.Cleaner{
			RecordPath: p.conf.PathDefaults.RecordPath,
			PathConfs:  p.conf.Paths,
			Parent:     p,
		}
		p.recordCleaner.Initialize()
	}

	if p.pathManager == nil {
		rtpMaxPayloadSize := getRTPMaxPayloadSize(p.conf.UDPMaxPayloadSize, p.conf.RTSPEncryption)

		p.pathManager = &pathManager{
			logLevel:          p.conf.LogLevel,
			authManager:       p.authManager,
			rtspAddress:       p.conf.RTSPAddress,
			readTimeout:       p.conf.ReadTimeout,
			writeTimeout:      p.conf.WriteTimeout,
			writeQueueSize:    p.conf.WriteQueueSize,
			rtpMaxPayloadSize: rtpMaxPayloadSize,
			pathConfs:         p.conf.Paths,
			externalCmdPool:   p.externalCmdPool,
			metrics:           p.metrics,
			parent:            p,
		}
		p.pathManager.initialize()
	}

	// RTSP Server
	if p.conf.RTSP &&
		(p.conf.RTSPEncryption == conf.EncryptionNo ||
			p.conf.RTSPEncryption == conf.EncryptionOptional) &&
		p.rtspServer == nil {
		_, useUDP := p.conf.RTSPTransports[gortsplib.ProtocolUDP]
		_, useMulticast := p.conf.RTSPTransports[gortsplib.ProtocolUDPMulticast]

		i := &rtsp.Server{
			Address:             p.conf.RTSPAddress,
			AuthMethods:         p.conf.RTSPAuthMethods,
			UDPReadBufferSize:   p.conf.RTSPUDPReadBufferSize,
			ReadTimeout:         p.conf.ReadTimeout,
			WriteTimeout:        p.conf.WriteTimeout,
			WriteQueueSize:      p.conf.WriteQueueSize,
			UseUDP:              useUDP,
			UseMulticast:        useMulticast,
			RTPAddress:          p.conf.RTPAddress,
			RTCPAddress:         p.conf.RTCPAddress,
			MulticastIPRange:    p.conf.MulticastIPRange,
			MulticastRTPPort:    p.conf.MulticastRTPPort,
			MulticastRTCPPort:   p.conf.MulticastRTCPPort,
			IsTLS:               false,
			ServerCert:          "",
			ServerKey:           "",
			RTSPAddress:         p.conf.RTSPAddress,
			Transports:          p.conf.RTSPTransports,
			RunOnConnect:        p.conf.RunOnConnect,
			RunOnConnectRestart: p.conf.RunOnConnectRestart,
			RunOnDisconnect:     p.conf.RunOnDisconnect,
			ExternalCmdPool:     p.externalCmdPool,
			Metrics:             p.metrics,
			PathManager:         p.pathManager,
			Parent:              p,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.rtspServer = i
	}

	// RTSPS Server
	if p.conf.RTSP &&
		(p.conf.RTSPEncryption == conf.EncryptionStrict ||
			p.conf.RTSPEncryption == conf.EncryptionOptional) &&
		p.rtspsServer == nil {
		_, useUDP := p.conf.RTSPTransports[gortsplib.ProtocolUDP]
		_, useMulticast := p.conf.RTSPTransports[gortsplib.ProtocolUDPMulticast]

		i := &rtsp.Server{
			Address:             p.conf.RTSPSAddress,
			AuthMethods:         p.conf.RTSPAuthMethods,
			UDPReadBufferSize:   p.conf.RTSPUDPReadBufferSize,
			ReadTimeout:         p.conf.ReadTimeout,
			WriteTimeout:        p.conf.WriteTimeout,
			WriteQueueSize:      p.conf.WriteQueueSize,
			UseUDP:              useUDP,
			UseMulticast:        useMulticast,
			RTPAddress:          p.conf.SRTPAddress,
			RTCPAddress:         p.conf.SRTCPAddress,
			MulticastIPRange:    p.conf.MulticastIPRange,
			MulticastRTPPort:    p.conf.MulticastSRTPPort,
			MulticastRTCPPort:   p.conf.MulticastSRTCPPort,
			IsTLS:               true,
			ServerCert:          p.conf.RTSPServerCert,
			ServerKey:           p.conf.RTSPServerKey,
			RTSPAddress:         p.conf.RTSPAddress,
			Transports:          p.conf.RTSPTransports,
			RunOnConnect:        p.conf.RunOnConnect,
			RunOnConnectRestart: p.conf.RunOnConnectRestart,
			RunOnDisconnect:     p.conf.RunOnDisconnect,
			ExternalCmdPool:     p.externalCmdPool,
			Metrics:             p.metrics,
			PathManager:         p.pathManager,
			Parent:              p,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.rtspsServer = i
	}

	// RTMP Server
	if p.conf.RTMP &&
		(p.conf.RTMPEncryption == conf.EncryptionNo ||
			p.conf.RTMPEncryption == conf.EncryptionOptional) &&
		p.rtmpServer == nil {
		i := &rtmp.Server{
			Address:             p.conf.RTMPAddress,
			ReadTimeout:         p.conf.ReadTimeout,
			WriteTimeout:        p.conf.WriteTimeout,
			IsTLS:               false,
			ServerCert:          "",
			ServerKey:           "",
			RTSPAddress:         p.conf.RTSPAddress,
			RunOnConnect:        p.conf.RunOnConnect,
			RunOnConnectRestart: p.conf.RunOnConnectRestart,
			RunOnDisconnect:     p.conf.RunOnDisconnect,
			ExternalCmdPool:     p.externalCmdPool,
			Metrics:             p.metrics,
			PathManager:         p.pathManager,
			Parent:              p,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.rtmpServer = i
	}

	// RTMPS Server
	if p.conf.RTMP &&
		(p.conf.RTMPEncryption == conf.EncryptionStrict ||
			p.conf.RTMPEncryption == conf.EncryptionOptional) &&
		p.rtmpsServer == nil {
		i := &rtmp.Server{
			Address:             p.conf.RTMPSAddress,
			ReadTimeout:         p.conf.ReadTimeout,
			WriteTimeout:        p.conf.WriteTimeout,
			IsTLS:               true,
			ServerCert:          p.conf.RTMPServerCert,
			ServerKey:           p.conf.RTMPServerKey,
			RTSPAddress:         p.conf.RTSPAddress,
			RunOnConnect:        p.conf.RunOnConnect,
			RunOnConnectRestart: p.conf.RunOnConnectRestart,
			RunOnDisconnect:     p.conf.RunOnDisconnect,
			ExternalCmdPool:     p.externalCmdPool,
			Metrics:             p.metrics,
			PathManager:         p.pathManager,
			Parent:              p,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.rtmpsServer = i
	}

	// WebRTC Server
	if p.conf.WebRTC && p.webRTCServer == nil {
		i := &webrtc.Server{
			Address:               p.conf.WebRTCAddress,
			Encryption:            p.conf.WebRTCEncryption,
			ServerKey:             p.conf.WebRTCServerKey,
			ServerCert:            p.conf.WebRTCServerCert,
			AllowOrigin:           p.conf.WebRTCAllowOrigin,
			TrustedProxies:        p.conf.WebRTCTrustedProxies,
			ReadTimeout:           p.conf.ReadTimeout,
			WriteTimeout:          p.conf.WriteTimeout,
			LocalUDPAddress:       p.conf.WebRTCLocalUDPAddress,
			LocalTCPAddress:       p.conf.WebRTCLocalTCPAddress,
			IPsFromInterfaces:     p.conf.WebRTCIPsFromInterfaces,
			IPsFromInterfacesList: p.conf.WebRTCIPsFromInterfacesList,
			AdditionalHosts:       p.conf.WebRTCAdditionalHosts,
			ICEServers:            p.conf.WebRTCICEServers2,
			HandshakeTimeout:      p.conf.WebRTCHandshakeTimeout,
			STUNGatherTimeout:     p.conf.WebRTCSTUNGatherTimeout,
			TrackGatherTimeout:    p.conf.WebRTCTrackGatherTimeout,
			ExternalCmdPool:       p.externalCmdPool,
			Metrics:               p.metrics,
			PathManager:           p.pathManager,
			Parent:                p,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.webRTCServer = i
	}

	// R-Video Server
	if p.conf.CodecServerAddress != "" && p.rvideoServer == nil {
		rvideoServer, err := rvideo.NewRVideoServer(p.conf.CodecServerAddress, p)
		if err != nil {
			return err
		}
		p.rvideoServer = rvideoServer
		p.Log(logger.Info, "R-Video Server version=%s", rvideoServer.Version)
	}

	// Record Manager
	if p.recordManager == nil {
		i := &recorder.Manager{
			RecordPath:   p.conf.PathDefaults.RecordPath,
			APIDomain:    p.conf.APIDomain,
			APIAddress:   p.conf.APIAddress,
			PathConfs:    p.conf.Paths,
			PathDefaults: &p.conf.PathDefaults,
			PathManager:  p.pathManager,
			Parent:       p,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.recordManager = i

		// Set the recordManager in pathManager for pathNotReady callback
		p.pathManager.recordManager = i
	}

	// API Auth Middleware
	if p.conf.APIAuth && p.authMiddleware == nil {
		keys := map[string]string{
			p.conf.AppID: p.conf.AppSecret,
		}
		var keyProvider = livekitauth.NewFileBasedKeyProviderFromMap(keys)
		p.authMiddleware = proapi.NewAPIKeyAuthMiddleware(keyProvider)
		p.Log(logger.Info, "API auth middleware initialized with AppID: %s", p.conf.AppID)
	}

	// API
	if p.conf.API && p.api == nil {
		i := &proapi.APIV2{
			Version:           Version,
			Started:           started,
			Address:           p.conf.APIAddress,
			Encryption:        p.conf.APIEncryption,
			ServerKey:         p.conf.APIServerKey,
			ServerCert:        p.conf.APIServerCert,
			AllowOrigin:       p.conf.APIAllowOrigin,
			TrustedProxies:    p.conf.APITrustedProxies,
			ReadTimeout:       p.conf.ReadTimeout,
			WriteTimeout:      p.conf.WriteTimeout,
			Conf:              p.conf,
			AuthManager:       p.authManager,
			PathManager:       p.pathManager,
			RTSPServer:        p.rtspServer,
			RTSPSServer:       p.rtspsServer,
			RTMPServer:        p.rtmpServer,
			RTMPSServer:       p.rtmpsServer,
			WebRTCServer:      p.webRTCServer,
			RecordManager:     p.recordManager,
			Parent:            p,
			APIAuthMiddleware: p.authMiddleware,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.api = i
	}

	// Initialize Smart Recording (requires API for color checking)
	if p.recordManager != nil && p.api != nil {
		err = p.recordManager.InitializeSmartRecording(p.api)
		if err != nil {
			return err
		}
	}

	// Health Checker (requires API for snapshot functionality)
	if p.healthChecker == nil && p.api != nil {
		i := &healthcheck.Checker{
			PathConfs:   p.conf.Paths,
			PathManager: p.pathManager,
			Parent:      p,
		}
		err = i.Initialize(p.api)
		if err != nil {
			return err
		}
		p.healthChecker = i
	}

	if initial && p.confPath != "" {
		cf := &confwatcher.ConfWatcher{FilePath: p.confPath}
		err = cf.Initialize()
		if err != nil {
			return err
		}
		p.confWatcher = cf
	}

	return nil
}

func (p *Core) closeResources(newConf *conf.Conf, calledByAPI bool) {
	closeLogger := newConf == nil ||
		newConf.LogLevel != p.conf.LogLevel ||
		!reflect.DeepEqual(newConf.LogDestinations, p.conf.LogDestinations) ||
		newConf.LogFile != p.conf.LogFile ||
		newConf.SysLogPrefix != p.conf.SysLogPrefix

	closeAuthManager := newConf == nil ||
		newConf.AuthMethod != p.conf.AuthMethod ||
		newConf.AuthHTTPAddress != p.conf.AuthHTTPAddress ||
		!reflect.DeepEqual(newConf.AuthHTTPExclude, p.conf.AuthHTTPExclude) ||
		newConf.AuthJWTJWKS != p.conf.AuthJWTJWKS ||
		newConf.AuthJWTJWKSFingerprint != p.conf.AuthJWTJWKSFingerprint ||
		newConf.AuthJWTClaimKey != p.conf.AuthJWTClaimKey ||
		!reflect.DeepEqual(newConf.AuthJWTExclude, p.conf.AuthJWTExclude) ||
		newConf.AuthJWTInHTTPQuery != p.conf.AuthJWTInHTTPQuery ||
		newConf.ReadTimeout != p.conf.ReadTimeout
	if !closeAuthManager && !reflect.DeepEqual(newConf.AuthInternalUsers, p.conf.AuthInternalUsers) {
		p.authManager.ReloadInternalUsers(newConf.AuthInternalUsers)
	}

	closeMetrics := newConf == nil ||
		newConf.Metrics != p.conf.Metrics ||
		closeAuthManager ||
		closeLogger

	closeRecorderCleaner := newConf == nil ||
		atLeastOneRecordClearDaysAgo(newConf.Paths) != atLeastOneRecordClearDaysAgo(p.conf.Paths) ||
		newConf.PathDefaults.RecordPath != p.conf.PathDefaults.RecordPath ||
		closeLogger
	if !closeRecorderCleaner && p.recordCleaner != nil && !reflect.DeepEqual(newConf.Paths, p.conf.Paths) {
		p.recordCleaner.ReloadPathConfs(newConf.Paths)
	}

	closePathManager := newConf == nil ||
		newConf.LogLevel != p.conf.LogLevel ||
		closeMetrics ||
		closeAuthManager ||
		closeLogger
	if !closePathManager && !reflect.DeepEqual(newConf.Paths, p.conf.Paths) {
		p.pathManager.ReloadPathConfs(newConf.Paths)
	}

	closeRTSPServer := newConf == nil ||
		newConf.RTSP != p.conf.RTSP ||
		closeMetrics ||
		closePathManager ||
		closeLogger

	closeRTMPServer := newConf == nil ||
		newConf.RTMP != p.conf.RTMP ||
		closeMetrics ||
		closePathManager ||
		closeLogger

	closeWebRTCServer := newConf == nil ||
		newConf.WebRTC != p.conf.WebRTC ||
		closeMetrics ||
		closePathManager ||
		closeLogger

	closeRecordManager := newConf == nil ||
		closePathManager ||
		closeLogger
	if !closeRecordManager && p.recordManager != nil && !reflect.DeepEqual(newConf.Paths, p.conf.Paths) {
		p.recordManager.ReloadPathConfs(newConf.Paths)
	}

	closeAPI := newConf == nil ||
		newConf.API != p.conf.API ||
		closeAuthManager ||
		closePathManager ||
		closeRTSPServer ||
		closeRTMPServer ||
		closeWebRTCServer ||
		closeRecordManager ||
		closeLogger

	closeHealthChecker := newConf == nil ||
		closeAPI ||
		closePathManager ||
		closeLogger
	if !closeHealthChecker && p.healthChecker != nil && !reflect.DeepEqual(newConf.Paths, p.conf.Paths) {
		p.healthChecker.ReloadPathConfs(newConf.Paths, p.api)
	}

	if newConf == nil && p.confWatcher != nil {
		p.confWatcher.Close()
		p.confWatcher = nil
	}

	if closeHealthChecker && p.healthChecker != nil {
		p.healthChecker.Close()
		p.healthChecker = nil
	}

	if p.api != nil {
		if closeAPI {
			p.api.Close()
			p.api = nil
		} else if !calledByAPI {
			p.api.ReloadConf(newConf)
		}
	}

	if closeWebRTCServer && p.webRTCServer != nil {
		p.webRTCServer.Close()
		p.webRTCServer = nil
	}

	if closeRecordManager && p.recordManager != nil {
		p.recordManager.Close()
		p.recordManager = nil
	}

	if p.rtmpsServer != nil && (closeRTMPServer || newConf == nil) {
		p.rtmpsServer.Close()
		p.rtmpsServer = nil
	}

	if p.rtmpServer != nil && (closeRTMPServer || newConf == nil) {
		p.rtmpServer.Close()
		p.rtmpServer = nil
	}

	if p.rtspsServer != nil && (closeRTSPServer || newConf == nil) {
		p.rtspsServer.Close()
		p.rtspsServer = nil
	}

	if p.rtspServer != nil && (closeRTSPServer || newConf == nil) {
		p.rtspServer.Close()
		p.rtspServer = nil
	}

	if closePathManager && p.pathManager != nil {
		p.pathManager.close()
		p.pathManager = nil
	}

	if closeRecorderCleaner && p.recordCleaner != nil {
		p.recordCleaner.Close()
		p.recordCleaner = nil
	}

	if closeMetrics && p.metrics != nil {
		p.metrics.Close()
		p.metrics = nil
	}

	if closeAuthManager && p.authManager != nil {
		p.authManager = nil
	}

	if newConf == nil && p.externalCmdPool != nil {
		p.Log(logger.Info, "waiting for running hooks")
		p.externalCmdPool.Close()
	}

	if closeLogger && p.logger != nil {
		p.logger.Close()
		p.logger = nil
	}
}

func (p *Core) reloadConf(newConf *conf.Conf, calledByAPI bool) error {
	p.closeResources(newConf, calledByAPI)
	p.conf = newConf
	return p.createResources(false)
}

// Wait waits for the Core to exit.
func (p *Core) Wait() {
	<-p.done
}

// Close closes Core and waits for all goroutines to return.
func (p *Core) Close() {
	p.ctxCancel()
	<-p.done
}

// APIConfigSet is called by API.
func (p *Core) APIConfigSet(conf *conf.Conf) {
	select {
	case p.chAPIConfigSet <- conf:
	case <-p.ctx.Done():
	}
}

func (p *Core) ValidateKey(checkExpire bool) {
	if p.conf.CoreServerKey == "" {
		panic("validate coreServerKey required")
	}

	addrs := p.getMacAddr()
	if len(addrs) == 0 {
		panic("failed to get MAC address")
	}

	decText, err := Decrypt(p.conf.CoreServerKey, CoreSecret)
	if err != nil {
		panic("validate coreServerKey decrypt failed: " + err.Error())
	}

	// 解析密钥格式: MAC地址#过期日期#域名#固定密钥
	res := strings.Split(decText, "#")
	if len(res) != 4 {
		panic("validate coreServerKey format invalid")
	}

	macAddress := res[0]
	expireDateStr := res[1]
	// domain := res[2]
	fixedKey := res[3]

	// 验证固定密钥
	if fixedKey != "sh@021" {
		panic("validate coreServerKey signature invalid")
	}

	// 验证 MAC 地址
	if !contains(addrs, strings.ToUpper(macAddress)) {
		panic("validate macAddress mismatch: required=" + macAddress + ", current=" + addrs[0])
	}

	// 是否检查过期时间
	if checkExpire {
		expireDate, err := time.Parse("20060102", expireDateStr)
		if err != nil {
			panic("validate coreServerKey expireDate parse failed: " + err.Error())
		}

		nowDate := time.Now()
		if expireDate.Unix() <= nowDate.Unix() {
			panic("validate coreServerKey expired: " + expireDateStr)
		}
	}

	p.Log(logger.Info, "validate coreServerKey success")
}

// getMacAddr 获取本机所有网卡的 MAC 地址
func (p *Core) getMacAddr() (addrs []string) {
	interfaces, err := net.Interfaces()
	if err == nil {
		for _, i := range interfaces {
			if i.Flags&net.FlagUp != 0 && len(i.HardwareAddr) > 0 {
				// 只获取启动的网卡且有真实 MAC 地址
				addr := i.HardwareAddr.String()
				addrs = append(addrs, strings.ToUpper(addr))
			}
		}
	}
	return addrs
}

// contains 检查字符串切片中是否包含指定字符串
func contains(slice []string, item string) bool {
	item = strings.ToUpper(item)
	for _, s := range slice {
		if strings.ToUpper(s) == item {
			return true
		}
	}
	return false
}
