package websocket

import (
 "context"
 "net"
 "time"
)

// DialBounded is used only by the MCU lifecycle. The caller is its stable dial
// worker (the bridge runner itself); no task is allocated for the handshake.
// Native TinyGo TCP ignores ctx: its selected backend supplies the TCP dial
// bound. The handshake has a network deadline and cannot return async success.
func(config *Config) DialBounded(ctx context.Context, timeout time.Duration, connected func(net.Conn))(*Conn,error){
 return config.dialBounded(ctx, timeout, connected, nil)
}

// DialBoundedWithBuffers is the serialized MCU lifecycle path; the caller owns
// buffers for the full client lifetime and joins old generations before entry.
func(config *Config) DialBoundedWithBuffers(ctx context.Context, timeout time.Duration, connected func(net.Conn), buffers *DialBuffers)(*Conn,error){
 return config.dialBounded(ctx, timeout, connected, buffers)
}

func(config *Config) dialBounded(ctx context.Context, timeout time.Duration, connected func(net.Conn), buffers *DialBuffers)(*Conn,error){
 if ctx.Err()!=nil{return nil,ctx.Err()}
 if config.Location==nil{return nil,&DialError{config,ErrBadWebSocketLocation}}
 if config.Origin==nil{return nil,&DialError{config,ErrBadWebSocketOrigin}}
 dialer:=config.Dialer
 if dialer==nil {dialer=&net.Dialer{Timeout:timeout}}
 var conn net.Conn
 var err error
 if config.OwnedDialContext!=nil {
  if config.Location.Scheme!="ws" {return nil,&DialError{config,ErrBadScheme}}
  conn,err=config.OwnedDialContext(ctx,"tcp",parseAuthority(config.Location))
 } else {conn,err=dialWithDialer(ctx,dialer,config)}
 if err!=nil{return nil,&DialError{config,err}}
 conn,err=ownGenerationTransport(conn)
 if err!=nil{_=conn.Close();return nil,err}
 connected(conn)
 success:=false
 defer func(){if !success {_=conn.Close()}}()
 deadline:=time.Now().Add(timeout)
 if d,ok:=ctx.Deadline();ok&&d.Before(deadline){deadline=d}
 if err=conn.SetDeadline(deadline);err!=nil{return nil,err}
 ws,err:=newClientWithBuffers(config,conn,buffers)
 if err!=nil{return nil,&DialError{config,err}}
 if ctx.Err()!=nil{return nil,ctx.Err()}
 if err=conn.SetDeadline(time.Time{});err!=nil{return nil,err}
 success=true
 return ws,nil
}

// AbortTransport interrupts only this generation; it bypasses the WebSocket
// close-frame write. Hosted lowmem fixtures retain ordinary native Close.
func(ws *Conn) AbortTransport()error{return ws.rwc.Close()}
