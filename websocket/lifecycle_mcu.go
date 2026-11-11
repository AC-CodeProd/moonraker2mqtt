//go:build tinygo || lowmem

package websocket

import (
 "context"
 "fmt"
 "io"
 "net/http"
 "net"
 "errors"
 "net/url"
 "sync"
 "time"
 wire "moonraker2mqtt/websocket/wire"
)

const reusableWorkers = true

// This owner is never replaced on reconnect. Only the first successful Connect
// starts the two tasks; Disconnect is terminal for this client instance.
type platformLifecycle struct {
 mu sync.Mutex
 buffers wire.DialBuffers
 native nativeDialOwner
 nativeErr error
 started, closed bool
 readStart, writeStart chan *connectionGeneration
 readExit, writeExit chan struct{}
 generation *connectionGeneration
 cancelStart chan *dialCancellation
 cancelExit chan struct{}
}
type dialCancellation struct {
 ctx context.Context
 raw chan net.Conn
 finished,joined chan struct{}
}

// One cancellation worker, allocated before the first dial, reused for every
// attempt. It owns a raw connection only until that handshake has returned.
func(c *WebSocketClient) reusableCancelWorker(){
 defer close(c.lifecycle.cancelExit)
 for a:=range c.lifecycle.cancelStart {
  select{case<-a.finished:case<-a.ctx.Done():
   select{case raw:=<-a.raw:_=raw.Close();case<-a.finished:}
  }
  close(a.joined)
 }
}

type connectionGeneration struct {
 conn *wire.Conn
 stop, readDone, writeDone chan struct{}
 once sync.Once
}

func (c *WebSocketClient) stopGeneration(g *connectionGeneration) {
 g.once.Do(func(){
  c.stateMux.Lock()
  c.setState(WEB_SOCKET_STATE_STOPPED)
  close(g.stop)
  c.stateMux.Unlock()
  // Interrupt the explicitly owned transport, without a WebSocket close write.
  // Join both operations below before the next fresh native active allocation.
  _ = g.conn.AbortTransport()
 })
}
func(c *WebSocketClient) retireGeneration() {
 l:=&c.lifecycle
 if g:=l.generation;g!=nil {
  c.stopGeneration(g)
  <-g.readDone; <-g.writeDone
  // Workers cannot dispatch old responses after these joins. Requests either
  // already returned on stop or will delete their own entries; delete now as
  // well so the next generation cannot inherit slots.
  c.requestsMux.Lock()
  for id:=range c.requests { delete(c.requests,id) }
  c.requestsMux.Unlock()
  for { select {case <-c.sendChan: default: l.generation=nil;c.conn=nil;l.retireNative();return} }
 }
}
func(c *WebSocketClient) connectReusable(ctx context.Context) error {
 l:=&c.lifecycle;l.mu.Lock();defer l.mu.Unlock()
 if l.closed {return fmt.Errorf("WebSocket client permanently closed")}
 if l.nativeErr!=nil{return l.nativeErr}
 if ctx.Err()!=nil {return ctx.Err()}
 if c.IsConnected(){return NewClientAlreadyConnectedError("client already connecting or connected")}
 c.retireGeneration()
 if l.nativeErr!=nil{return l.nativeErr}
 l.buffers.Release()
 c.stateMux.Lock();c.setState(WEB_SOCKET_STATE_CONNECTING);c.stateMux.Unlock()
 wsURL:=c.config.GetWebSocketURL()
 u,err:=url.Parse(wsURL)
 if err!=nil { c.stateMux.Lock();c.setState(WEB_SOCKET_STATE_STOPPED);c.stateMux.Unlock();return err }
 cfg,err:=wire.NewConfig(wsURL,"http://"+u.Host)
 if err!=nil { c.stateMux.Lock();c.setState(WEB_SOCKET_STATE_STOPPED);c.stateMux.Unlock();return err }
 if c.config.APIKey!="" {cfg.Header=http.Header{};cfg.Header.Set("X-Api-Key",c.config.APIKey)}
 if l.native!=nil {cfg.OwnedDialContext=l.native.DialContext}
 if l.cancelStart==nil {
  l.cancelStart=make(chan *dialCancellation);l.cancelExit=make(chan struct{})
  go c.reusableCancelWorker()
 }
 attempt:=&dialCancellation{ctx:ctx,raw:make(chan net.Conn,1),finished:make(chan struct{}),joined:make(chan struct{})}
 l.cancelStart<-attempt
 conn,err:=cfg.DialBoundedWithBuffers(ctx,30*time.Second,func(raw net.Conn){attempt.raw<-raw},&l.buffers)
 close(attempt.finished);<-attempt.joined
 if err!=nil {l.retireNative();l.buffers.Release();c.stateMux.Lock();c.setState(WEB_SOCKET_STATE_STOPPED);c.stateMux.Unlock();return err}
 if ctx.Err()!=nil {_=conn.AbortTransport();l.retireNative();l.buffers.Release();c.stateMux.Lock();c.setState(WEB_SOCKET_STATE_STOPPED);c.stateMux.Unlock();return ctx.Err()}
 configureConnection(conn)
 if !l.started {
  l.readStart=make(chan *connectionGeneration);l.writeStart=make(chan *connectionGeneration)
  l.readExit=make(chan struct{});l.writeExit=make(chan struct{})
  l.started=true
  go c.reusableReadWorker()
  go c.reusableWriteWorker()
 }
 g:=&connectionGeneration{conn:conn,stop:make(chan struct{}),readDone:make(chan struct{}),writeDone:make(chan struct{})}
 c.stateMux.Lock();l.generation=g;c.conn=conn;c.setState(WEB_SOCKET_STATE_CONNECTED);c.stateMux.Unlock()
 l.readStart<-g;l.writeStart<-g
 c.retry.Reset()
 c.logger.Info("Connected to Moonraker at %s",wsURL)
 return nil
}
func(c *WebSocketClient) disconnectReusable() error {
 l:=&c.lifecycle;l.mu.Lock();defer l.mu.Unlock()
 if l.closed{return nil};l.closed=true
 c.retireGeneration()
 l.buffers.Release()
 if l.started {close(l.readStart);close(l.writeStart);<-l.readExit;<-l.writeExit}
 if l.cancelStart!=nil {close(l.cancelStart);<-l.cancelExit}
 return nil
}
func(c *WebSocketClient) reusableReadWorker(){
 defer close(c.lifecycle.readExit)
 for g:=range c.lifecycle.readStart {
  c.readGeneration(g)
  close(g.readDone)
 }
}
func(c *WebSocketClient) readGeneration(g *connectionGeneration){
 defer c.stopGeneration(g)
 for {
  select {case <-g.stop:return;default:}
  var m WebSocketMessage
  if err:=c.receiveMessage(g.conn,&m);err!=nil {
   if err==io.EOF {c.logger.Info("Connection closed by server")} else {
    select {case <-g.stop:default:
 var ne net.Error
 if errors.As(err,&ne)||errors.Is(err,io.ErrUnexpectedEOF)||errors.Is(err,net.ErrClosed){c.logger.Info("Moonraker transport unavailable")}else{c.logger.Error("Read error: %v, State: %s",err,c.GetState());if c.listener!=nil{c.listener.OnException(NewWebSocketError("read error",err))}}
 }
   }
   return
  }
  select {case <-g.stop:return;default:c.handleMessage(&m)}
 }
}
func(c *WebSocketClient) reusableWriteWorker(){
 defer close(c.lifecycle.writeExit)
 for g:=range c.lifecycle.writeStart {c.writeGeneration(g);close(g.writeDone)}
}
func(c *WebSocketClient) writeGeneration(g *connectionGeneration){
 defer c.stopGeneration(g)
 for {
  select {case <-g.stop:return;case m:=<-c.sendChan:
   select {case <-g.stop:return;default:}
   if err:=g.conn.SetWriteDeadline(time.Now().Add(30*time.Second));err!=nil{return}
   if err:=wire.JSON.Send(g.conn,m);err!=nil{return}
  }
 }
}
func(c *WebSocketClient) requestReusable(ctx context.Context,method string,params any,timeout time.Duration,typed bool)(*WebSocketMessage,error){
 if ctx.Err()!=nil{return nil,ctx.Err()}
 // Atomic state check/registration/enqueue against generation stop. Never
 // hold this lock while waiting for a response or doing network I/O.
 c.stateMux.RLock()
 if c.state!=WEB_SOCKET_STATE_CONNECTED {c.stateMux.RUnlock();return nil,NewWebSocketNotConnectedError("not connected")}
 g:=c.lifecycle.generation
 c.requestsMux.Lock()
 if len(c.requests)>=maxPendingRequests {c.requestsMux.Unlock();c.stateMux.RUnlock();return nil,NewWebSocketError("pending request limit reached",nil)}
 id:=c.nextID;c.nextID++
 req:=&WebSocketRequest{ID:id,Method:method,Params:params,Response:make(chan WebSocketResponse,1)}
 req.setKlippyState(typed);c.requests[id]=req
 c.requestsMux.Unlock()
 m:=&WebSocketMessage{ID:&id,Method:method,Params:params,JSONRPC:"2.0"}
 queued:=false
 select {case c.sendChan<-m:queued=true;default:}
 c.stateMux.RUnlock()
 defer func(){c.requestsMux.Lock();delete(c.requests,id);c.requestsMux.Unlock()}()
 if !queued {return nil,NewWebSocketError("send buffer full",nil)}
 timer:=time.NewTimer(timeout);defer timer.Stop()
 select {
 case <-ctx.Done():return nil,ctx.Err()
 case <-g.stop:return nil,NewWebSocketNotConnectedError("connection ended")
 case <-timer.C:return nil,NewWebSocketTimeoutError("request timeout")
 case r:=<-req.Response:
  if r.Error!=nil{return nil,r.Error}
  return &WebSocketMessage{Result:r.Result,JSONRPC:"2.0"},nil
 }
}

func(l *platformLifecycle) retireNative(){if l.native!=nil {l.nativeErr=l.native.Retire()}}
func(c *WebSocketClient) initLifecycle(){c.lifecycle.buffers.Init();c.lifecycle.native,c.lifecycle.nativeErr=newNativeDialOwner()}
