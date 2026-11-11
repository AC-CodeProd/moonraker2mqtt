package websocket

import (
 "net"
 "sync"
 "errors"
)

// Only the Moonraker bounded dial owns this wrapper, never MQTT or global netdev.
// Native active clients are freshly allocated, not pooled; all workers must join
// before the lifecycle drops this wrapper or dials a replacement generation.
type generationTransport struct {
 net.Conn
 interrupt func() error
 once sync.Once
 closeErr error
}
func(c *generationTransport)Close()error{
 c.once.Do(func(){_ = c.interrupt();c.closeErr=c.Conn.Close()})
 return c.closeErr
}
func ownGenerationTransport(c net.Conn)(net.Conn,error){
 if p,ok:=c.(interface{GenerationInterrupt()(func()error,error)});ok{
  interrupt,err:=p.GenerationInterrupt()
  if err!=nil{return c,err}
  return &generationTransport{Conn:c,interrupt:interrupt},nil
 }
 if requireNativeGeneration{return c,errors.New("native generation interruption required")}
 return c,nil
}
