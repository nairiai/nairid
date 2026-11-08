// Package socketio provides Engine.IO polling, adapted from github.com/zishang520/socket.io (MIT).
// One lock covers pause registration, pending writes, and the next-poll decision to avoid missed completions.
package socketio

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/zishang520/socket.io/clients/engine/v3"
	"github.com/zishang520/socket.io/parsers/engine/v3/packet"
	"github.com/zishang520/socket.io/parsers/engine/v3/parser"
	"github.com/zishang520/socket.io/v3/pkg/log"
	"github.com/zishang520/socket.io/v3/pkg/request"
	"github.com/zishang520/socket.io/v3/pkg/types"
)

const pollingName = "polling"

// defaultPauseTimeout matches the server's default upgrade timeout, so a pause held longer has failed.
const defaultPauseTimeout = 10 * time.Second

var pollingLog = log.NewLog("engine.io-client:polling")

// Polling is the transport constructor to pass to SetTransports in place of the library's.
var Polling engine.TransportCtor = pollingCtor{pauseTimeout: defaultPauseTimeout}

type pollingCtor struct {
	pauseTimeout time.Duration
}

func (pollingCtor) Name() string { return pollingName }

func (c pollingCtor) New(socket engine.Socket, opts engine.SocketOptionsInterface) engine.Transport {
	p := &polling{Transport: engine.MakeTransport(), pauseTimeout: c.pauseTimeout}
	p.Prototype(p)
	p.Construct(socket, opts)
	return p
}

type polling struct {
	engine.Transport

	client       *request.HTTPClient
	pauseTimeout time.Duration

	mu      sync.Mutex
	polling bool
	writing bool
	onPause func()
}

func (p *polling) Name() string { return pollingName }

func (p *polling) Construct(socket engine.Socket, opts engine.SocketOptionsInterface) {
	p.Transport.Construct(socket, opts)
	p.client = request.NewHTTPClient(
		request.WithLogger(engine.NewLog("HTTPClient")),
		request.WithTimeout(p.Opts().RequestTimeout()),
		request.WithCookieJar(p.Socket().CookieJar()),
		request.WithTransport(request.NewTransport(p.Opts().TLSClientConfig(), p.Opts().QUICConfig())),
	)
}

func (p *polling) DoOpen() {
	p.mu.Lock()
	p.polling = true
	p.mu.Unlock()
	p.startPoll()
}

func (p *polling) startPoll() {
	pollingLog.Debug("polling")
	p.Emit("poll")
	go p.doPoll()
}

// Pause runs onPause once no request is in flight. The upgrade calls it from the probe's goroutine
// while a poll may be completing on another, so the check and the registration are one step.
func (p *polling) Pause(onPause func()) {
	p.mu.Lock()
	p.SetReadyState(engine.TransportStatePausing)
	p.onPause = onPause
	fire := p.takePauseLocked()
	p.mu.Unlock()

	time.AfterFunc(p.pauseTimeout, p.abandonStuckPause)
	if fire == nil {
		pollingLog.Debug("waiting to pause")
		return
	}
	fire()
}

// takePauseLocked returns the pending pause callback once nothing is in flight, at most once.
func (p *polling) takePauseLocked() func() {
	if p.onPause == nil || p.polling || p.writing {
		return nil
	}
	fire := p.onPause
	p.onPause = nil
	p.SetReadyState(engine.TransportStatePaused)
	pollingLog.Debug("paused")
	return fire
}

// abandonStuckPause closes a transport that is still paused or pausing as the session's transport.
// A failed probe leaves polling paused with no way back, so nairid reconnects instead of waiting
// for the ping timeout.
func (p *polling) abandonStuckPause() {
	state := p.ReadyState()
	if state != engine.TransportStatePausing && state != engine.TransportStatePaused {
		return
	}
	if current := p.Socket().Transport(); current != engine.Transport(p) {
		return
	}
	pollingLog.Debug("upgrade did not complete - closing transport")
	p.OnClose(errors.New("upgrade did not complete"))
}

func (p *polling) OnData(data types.BufferInterface) {
	packets, _ := parser.Parserv4().DecodePayload(data)
	for _, pkt := range packets {
		p.onPacket(pkt)
	}

	p.mu.Lock()
	state := p.ReadyState()
	if state == engine.TransportStateClosed {
		p.mu.Unlock()
		return
	}
	// Claim the next poll under the lock so a concurrent Pause waits for it.
	again := state == engine.TransportStateOpen
	p.polling = again
	fire := p.takePauseLocked()
	p.mu.Unlock()

	p.Emit("pollComplete")
	if fire != nil {
		fire()
	}
	if again {
		p.startPoll()
	}
}

func (p *polling) onPacket(pkt *packet.Packet) {
	if p.ReadyState() == engine.TransportStateOpening && pkt.Type == packet.OPEN {
		p.OnOpen()
	}
	if pkt.Type == packet.CLOSE {
		p.OnClose(errors.New("transport closed by the server"))
		return
	}
	p.OnPacket(pkt)
}

// Send accepts packets until the pause completes, so a flush racing the upgrade is waited for
// rather than posted after the switch.
func (p *polling) Send(packets []*packet.Packet) {
	p.mu.Lock()
	state := p.ReadyState()
	if state != engine.TransportStateOpen && state != engine.TransportStatePausing {
		p.mu.Unlock()
		pollingLog.Debug("transport is not open, discarding packets")
		return
	}
	p.writing = true
	p.mu.Unlock()

	p.SetWritable(false)
	go p.write(packets)
}

func (p *polling) Write(packets []*packet.Packet) {
	p.Send(packets)
}

func (p *polling) write(packets []*packet.Packet) {
	data, _ := parser.Parserv4().EncodePayload(packets)
	if !p.fetch(data, "fetch write error") {
		return
	}

	p.mu.Lock()
	p.writing = false
	fire := p.takePauseLocked()
	p.mu.Unlock()

	p.SetWritable(true)
	p.Emit("drain")
	if fire != nil {
		fire()
	}
}

func (p *polling) DoClose() {
	defer func() { _ = p.client.Close() }()
	cleanup := func(...any) {
		pollingLog.Debug("writing close packet")
		p.Write([]*packet.Packet{{Type: packet.CLOSE}})
	}
	if p.ReadyState() == engine.TransportStateOpen {
		cleanup()
		return
	}
	_ = p.Once("open", cleanup)
}

func (p *polling) doPoll() {
	res, err := p.request(nil)
	if err != nil {
		p.OnError("fetch read error", err, nil)
		return
	}
	defer func() { _ = res.Body.Close() }()
	if !res.Ok() {
		p.OnError("fetch read error", res.Err, res.Request.Context())
		return
	}
	data, err := types.NewStringBufferReader(res.Body)
	if err != nil {
		p.OnError("fetch read error", err, nil)
		return
	}
	p.OnData(data)
}

// fetch posts data and reports whether the server accepted it.
func (p *polling) fetch(data io.Reader, reason string) bool {
	res, err := p.request(data)
	if err != nil {
		p.OnError(reason, err, nil)
		return false
	}
	defer func() { _ = res.Body.Close() }()
	if !res.Ok() {
		p.OnError(reason, res.Err, res.Request.Context())
		return false
	}
	return true
}

func (p *polling) request(data io.Reader) (*request.Response, error) {
	headers := http.Header{}
	for k, vs := range p.Opts().ExtraHeaders() {
		for _, v := range vs {
			headers.Add(k, v)
		}
	}
	if data == nil {
		return p.client.Get(p.uri().String(), &request.Options{Headers: headers})
	}
	headers.Set("Content-Type", "text/plain;charset=UTF-8")
	return p.client.Post(p.uri().String(), &request.Options{Body: data, Headers: headers})
}

func (p *polling) uri() *url.URL {
	schema := "http"
	if p.Opts().Secure() {
		schema = "https"
	}
	query := url.Values{}
	for k, vs := range p.Query() {
		for _, v := range vs {
			query.Add(k, v)
		}
	}
	if p.Opts().TimestampRequests() {
		query.Set(p.Opts().TimestampParam(), request.RandomString())
	}
	if !p.SupportsBinary() && !query.Has("sid") {
		query.Set("b64", "1")
	}
	return p.CreateUri(schema, query)
}
