package xnet

import (
 "errors"
 "net"
 "net/netip"
 "os"
 "runtime"
 "sync/atomic"
 "sync"
 "testing"
 "time"

 "github.com/soypat/lneto"
 "github.com/soypat/lneto/tcp"
)

// Exercises StackGo's REAL fresh active-client allocation and Berkeley API.
// No direct test-only Abort is used as the fixed-behaviour oracle.
func TestOwnedGenerationInterruptNative(t *testing.T) {
 client,server,_,sc:=newTCPStacks(t,5678,1500)
 backoff:=lneto.BackoffStrategy(func(uint)time.Duration{return 5*time.Millisecond})
 sg:=client.StackGo(backoff,StackGoConfig{ListenerPoolConfig:TCPPoolConfig{
  PoolSize:4,QueueSize:4,TxBufSize:4096,RxBufSize:1024,
  EstablishedTimeout:2*time.Second,ClosingTimeout:2*time.Second,
  NewBackoff:func()lneto.BackoffStrategy{return backoff},
 }})
 if err:=server.ListenTCP4(sc,8080);err!=nil{t.Fatal(err)}
 var drop atomic.Bool
 var ticks atomic.Int64
 stop,joined:=make(chan struct{}),make(chan struct{})
 go func(){
  defer close(joined)
  b:=make([]byte,1600)
  for {
   select{case<-stop:return;default:}
   n,_:=client.EgressEthernet(b);ticks.Add(1)
   if n>0&&!drop.Load(){_=server.IngressEthernet(b[:n])}
   n,_=server.EgressEthernet(b)
   if n>0&&!drop.Load(){_=client.IngressEthernet(b[:n])}
   time.Sleep(time.Millisecond)
  }
 }()
 var stopOnce sync.Once
 stopPump:=func(){stopOnce.Do(func(){close(stop);<-joined})}
 defer stopPump()
 stack:=NewBerkeleyStack(sg.Socket)
 fd,err:=stack.Socket(AF_INET,SOCK_STREAM,6);if err!=nil{t.Fatal(err)}
 if err=stack.Connect(fd,"",netip.AddrPortFrom(netip.AddrFrom4(server.Addr4()),8080));err!=nil{t.Fatal(err)}
 active:=stack.getConn(fd).sock.(tcpconn)
 cc:=active.Conn
 if cc.State()!=tcp.StateEstablished{t.Fatal("not established")}
 if cc.FreeOutput()!=4096{t.Fatal("production TX geometry not selected")}
 generation:=cc.Generation()
 interrupt,err:=stack.GenerationInterrupt(fd);if err!=nil{t.Fatal(err)}
 if _,err=stack.GenerationInterrupt(fd);err==nil{t.Fatal("duplicate capability issued")}
 // Reuse the SAME Berkeley/netdevice instance for an unrelated connection.
 // Its ordinary Write and Close must not be converted into Abort.
 mc:=new(tcp.Conn)
 if err=mc.Configure(tcp.ConnConfig{RxBuf:make([]byte,1024),TxBuf:make([]byte,4096),TxPacketQueueSize:4,RWBackoff:backoff});err!=nil{t.Fatal(err)}
 if err=mc.OpenActive(1883,netip.MustParseAddrPort("192.0.2.2:1883"),333);err!=nil{t.Fatal(err)}
 // This independent unestablished control is deliberately not a broker proof.
 // Native unchanged generation/state prove isolation; end-to-end MQTT is separate.
 mfd:=stack.newFD();stack.conns=append(stack.conns,socket[net.Conn]{sockfd:mfd,sock:tcpconn{Conn:mc}})
 mg:=mc.Generation();ms:=mc.State()
 if _,err=stack.GenerationInterrupt(mfd);err==nil{t.Fatal("pooled/unqualified socket accepted")}
 drop.Store(true)
 sendDone,readDone,closeDone:=make(chan error,1),make(chan error,1),make(chan error,1)
 go func(){_,e:=stack.Send(fd,make([]byte,4097),0,time.Now().Add(30*time.Second));sendDone<-e}()
 reached:=time.Now().Add(time.Second)
 for cc.FreeOutput()!=0&&time.Now().Before(reached){runtime.Gosched()}
 if cc.FreeOutput()!=0{t.Fatal("full TX not reached")}
 go func(){_,e:=stack.Recv(fd,make([]byte,1),0,time.Time{});readDone<-e}()
 go func(){closeDone<-stack.Close(fd)}()
 // Keep real egress active across the existing 10 s supervisor grace.
 before:=ticks.Load()
 timer:=time.NewTimer(10100*time.Millisecond)
 if os.Getenv("DISABLE_GENERATION_INTERRUPT")!="1" {
  // Deterministic native pump gate: the capability must not mutate connid
  // while a pump could read its opaque ConnectionID pointer.
  client.mu.Lock();interrupted:=make(chan error,1)
  go func(){interrupted<-interrupt()}()
  select{case e:=<-interrupted:client.mu.Unlock();t.Fatalf("interrupt bypassed stack generation lock: %v",e);case<-time.After(20*time.Millisecond):}
  client.mu.Unlock()
  if err=<-interrupted;err!=nil{t.Fatal(err)}
 }
 var sendJoined,readJoined,closeJoined bool
 for !(sendJoined&&readJoined&&closeJoined){
  select{
  case e:=<-sendDone:sendJoined=true;if e==nil{t.Error("blocked write returned success")}
  case e:=<-readDone:readJoined=true;if e==nil{t.Error("blocked read returned success")}
  case <-closeDone:closeJoined=true
  case <-timer.C:
   // OLD path cleanup also uses the API capability, not direct native Abort.
   if e:=interrupt();e!=nil{t.Log(e)}
   if !sendJoined{<-sendDone};if !readJoined{<-readDone};if !closeJoined{<-closeDone}
   t.Fatalf("FIXED_REQUIREMENT_RED: send=%t read=%t Close=%t before grace10s; egress=%d",sendJoined,readJoined,closeJoined,ticks.Load()-before)
  }
 }
 timer.Stop()
 if !errors.Is(interrupt(),net.ErrClosed){t.Fatal("repeated capability was not refused")}
 if mc.Generation()!=mg||mc.State()!=ms{t.Fatal("unrelated socket interrupted")}
 // After all operations joined, intentionally reuse the same native object.
 // The old captured capability must not affect the new native epoch.
 stopPump() // Join the pump before intentional test-only native instance reuse.
 if err=cc.OpenActive(1338,netip.MustParseAddrPort("192.0.2.1:8080"),777);err!=nil{t.Fatal(err)}
 ng:=cc.Generation()
 if ng==generation{t.Fatal("epoch did not change")}
 if !errors.Is(interrupt(),net.ErrClosed)||cc.Generation()!=ng{t.Fatal("stale capability affected reused instance")}
 t.Logf("JOINED_NATIVE_READ_WRITE_CLOSE; production TX4096 RX1024 queue4 backoff5ms; egress=%d; stale/repeated/duplicate/unqualified rejected",ticks.Load()-before)
}

func TestGenerationCapabilityRejectsWrongNativeEpoch(t *testing.T){
 _,_,cc,_:=newTCPStacks(t,5678,1500)
 if e:=cc.OpenActive(1337,netip.MustParseAddrPort("192.0.2.1:8080"),333);e!=nil{t.Fatal(e)}
 if _,e:=cc.ClaimGenerationInterrupt(cc.Generation()+1);!errors.Is(e,net.ErrClosed){t.Fatal("wrong epoch accepted",e)}
}
