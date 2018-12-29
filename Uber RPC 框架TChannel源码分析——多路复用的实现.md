![image](https://upload.cc/i1/2018/12/29/EZfJzo.png)

## 声明 
* [tchannel-go](https://github.com/uber/tchannel-go/tree/v1.12.0)版本为v1.12.0
* 阅读本篇文章需要go语言，HTTP2——多路复用基础

## 前言
> &nbsp;&nbsp;&nbsp;&nbsp;UBER的RPC框架TChannel有一个闪亮点————多路复用。对于多路复用是如何实现一直都很好奇，所以抽了点时间看了TChannel多路复用的实现源码，并整理成这篇文章。文章主要从客户端【发起请求】到服务端【响应请求】一条完整请求来看多路复用整个生命周期的实现。

## 客户端发起调用

> 客户端调用我们把这个过程分成4个步骤：

     1. 出站握手
     
     2. 复用链接
     
     3. 消息交换
     
     4. 有序写入——发起请求

### 出站握手
```
github.com/uber/tchannel-go/preinit_connection.go #35
func (ch *Channel) outboundHandshake(ctx context.Context, c net.Conn, outboundHP string, events connectionEvents) (_ *Connection, err error) {
  ......
  msg := &initReq{initMessage: ch.getInitMessage(ctx, 1)}
  if err := ch.writeMessage(c, msg); err != nil {
    return nil, err
  }
  ......
  res := &initRes{}
  id, err := ch.readMessage(c, res)
  if err != nil {
    return nil, err
  }
  ......

  return ch.newConnection(c, 1 /* initialID */, outboundHP, remotePeer, remotePeerAddress, events), nil
}
```
> &nbsp;&nbsp;&nbsp;&nbsp;在开始请求前，TChannel有一次握手，这次握手不是TCP/IP的三次握手，是为了确认服务端能够正常响应。 如果服务端能够正常响应，则这条TCP链接将会被复用。


```
func (ch *Channel) newConnection(conn net.Conn, initialID uint32, outboundHP string, remotePeer PeerInfo,
	remotePeerAddress peerAddressComponents, events connectionEvents) *Connection {
  ......
  connID := _nextConnID.Inc()
  ......
  c := &Connection{
    channelConnectionCommon: ch.channelConnectionCommon,

    connID:             connID,
    conn:               conn,
    opts:               opts,
    state:              connectionActive,
    sendCh:             make(chan *Frame, opts.SendBufferSize),
    ......
    inbound:            newMessageExchangeSet(log, messageExchangeSetInbound),
    outbound:           newMessageExchangeSet(log, messageExchangeSetOutbound),
    ......
  }

  ......
  // Connections are activated as soon as they are created.
  c.callOnActive()
  go c.readFrames(connID)
  go c.writeFrames(connID)
  return c
}
```

> &nbsp;&nbsp;&nbsp;&nbsp;当握手成功，这条链接随后会被放入Peer，以备其他请求使用。同时会启动2个协程，“readFrames” 用于读取服务端的响应，“writeFrames”把数据写入TCP链接里面，关于这2个协程的作用下面会详细介绍。


### 复用链接
```
github.com/uber/tchannel-go/peer.go #361
func (p *Peer) getActiveConnLocked() (*Connection, bool) {
  allConns := len(p.inboundConnections) + len(p.outboundConnections)
  if allConns == 0 {
    return nil, false
  }

  // We cycle through the connection list, starting at a random point
  // to avoid always choosing the same connection.
  startOffset := peerRng.Intn(allConns)
  for i := 0; i < allConns; i++ {
    connIndex := (i + startOffset) % allConns
    if conn := p.getConn(connIndex); conn.IsActive() {
      return conn, true
    }
  }

  return nil, false
}
```
> &nbsp;&nbsp;&nbsp;&nbsp;复用链接是多路复用很关键的一步，和HTTP的复用不同，HTTP链接需要响应成功后才能被复用，而多路复用链接只要被创建了就能被复用。

### 消息交换 —— 无序响应 
```
github.com/uber/tchannel-go/mex.go #306
func (mexset *messageExchangeSet) newExchange(ctx context.Context, framePool FramePool,
	msgType messageType, msgID uint32, bufferSize int) (*messageExchange, error) {
  ......
  mex := &messageExchange{
    msgType:   msgType,
    msgID:     msgID,
    ctx:       ctx,
    //请求会等待Frame的写入
    recvCh:    make(chan *Frame, bufferSize),
    errCh:     newErrNotifier(),
    mexset:    mexset,
    framePool: framePool,
  }

  mexset.Lock()
  //保存messageExchange
  addErr := mexset.addExchange(mex)
  mexset.Unlock()
  ......
  mexset.onAdded()

  ......
  return mex, nil
}
```
> &nbsp;&nbsp;&nbsp;&nbsp;在客户端发起多个请求的时候，由于只有一个TCP链接，如何知道哪个响应是对应哪个请求？为了能够正确响应，TChannel使用了MessageExchange，一个请求对应一个MessageExchange。客户端会以stream id 为下标索引，保存所有的MessageExchange。当有一个请求时，它会阻塞在MessageExchange.recvCh， 响应回来会根据响应的stream id获取对应的MessageExchange， 并把帧放到 MessageExchange.recvCh 从而实现无序响应。

### 有序写入——发起请求

#### 先写入队列

```
github.com/uber/tchannel-go/reqres.go #139
func (w *reqResWriter) flushFragment(fragment *writableFragment) error {
  ......
  frame := fragment.frame.(*Frame)
  ......
  select {
  ......
  case w.conn.sendCh <- frame:
    return nil
  }
}
```

#### 获取队列数据，写入TCP链接
```
github.com/uber/tchannel-go/connection.go #706
func (c *Connection) writeFrames(_ uint32) {
  for {
    select {
    case f := <-c.sendCh:
      ......
      err := f.WriteOut(c.conn)
      ......
    }
  }
}
```
>&nbsp;&nbsp;&nbsp;&nbsp;在多路复用中，只有一条TCP链接，为了避免客户端同时写入链接里，TChannel先把帧写入队列“sendCh”，再使用一个消费者获取队列数据，然后有序写入链接里面。

### 帧结构

```
github.com/uber/tchannel-go/frame.go #107
// A Frame is a header and payload
type Frame struct {
	buffer       []byte // full buffer, including payload and header
	headerBuffer []byte // slice referencing just the header

	// The header for the frame
	Header FrameHeader

	// The payload for the frame
	Payload []byte
}

// FrameHeader is the header for a frame, containing the MessageType and size
type FrameHeader struct {
	// The size of the frame including the header
	size uint16

	// The type of message represented by the frame
	messageType messageType

	// Left empty
	reserved1 byte

	// The id of the message represented by the frame
	ID uint32 //指Stream ID

	// Left empty
	reserved [8]byte
}
```

> &nbsp;&nbsp;&nbsp;&nbsp;帧被分为2部分，一部分是Header Frame（只有16字节）；另一部分是Data Frame。这2部分数据按照一定格式标准转成二进制数据进行传输。

## 服务端响应
>服务端响应我们把这个过程分成3个步骤：

     1. 入站握手
     
     2. 读取请求数据
     
     3. 有序写入——响应结果

### 入站握手

```
github.com/uber/tchannel-go/preinit_connection.go #69
func (ch *Channel) inboundHandshake(ctx context.Context, c net.Conn, events connectionEvents) (_ *Connection, err error) {
  id := uint32(math.MaxUint32)
  ......
  req := &initReq{}
  id, err = ch.readMessage(c, req)
  if err != nil {
    return nil, err
  }
  ......
  res := &initRes{initMessage: ch.getInitMessage(ctx, id)}
  if err := ch.writeMessage(c, res); err != nil {
    return nil, err
  }
  return ch.newConnection(c, 0 /* initialID */, "" /* outboundHP */, remotePeer, remotePeerAddress, events), nil
}
```
> &nbsp;&nbsp;&nbsp;&nbsp;入站握手是对客户端出站握手的响应，当握手成功，服务端这边也会调用newConnection，启动“readFrames” 和 “writeFrames”协程，等待客户端请求。


### 读取请求数据
```
github.com/uber/tchannel-go/connection.go #615
func (c *Connection) readFrames(_ uint32) {
  headerBuf := make([]byte, FrameHeaderSize)
  ......
  for {
    ......
    //先读头部
    if _, err := io.ReadFull(c.conn, headerBuf); err != nil {
      handleErr(err)
      return
    }
    frame := c.opts.FramePool.Get()

    if err := frame.ReadBody(headerBuf, c.conn); err != nil {
      handleErr(err)
      c.opts.FramePool.Release(frame)
      return
    }
    //handle  frame
    ......
  }
}

```
> &nbsp;&nbsp;&nbsp;&nbsp;在服务端会监听握手成功的链接，如果客户端发送了请求，就会读取链接里面的数据。读取分2步：

* 先读取Header Frame（16字节）
> &nbsp;&nbsp;&nbsp;&nbsp;Header Frame 的长度固定为16字节，这里面有stream Id 和 Data Frame的长度

* 再读取Data Frame
> &nbsp;&nbsp;&nbsp;&nbsp;从Header Frame获取到 Data Frame的长度后，根据长度从链接读取指定的字节长度，就获取到正确的Data Frame。

### 有序写入——响应结果

> &nbsp;&nbsp;&nbsp;&nbsp;服务端的有序写入和客户端的有序写入是一样的功能，只是所处的角色不一样，这里不再重复。


## 客户端获取响应结果

> 客户端获取响应结果我们把这个过程分成2个步骤：

     1. 读取响应结果
     
     2. 找到MessageExchange响应

### 读取响应结果
> &nbsp;&nbsp;&nbsp;&nbsp;客户端获取响应结果和服务端的读取请求数据也是相同的功能，这里不再重复。

### 找到MessageExchange响应

```
github.com/uber/tchannel-go/mex.go #429
func (mexset *messageExchangeSet) forwardPeerFrame(frame *Frame) error {
  ......
  mexset.RLock()
  mex := mexset.exchanges[frame.Header.ID]
  mexset.RUnlock()
  ......
  //把帧交给MessageExchange.recvCh
  if err := mex.forwardPeerFrame(frame); err != nil {
    ......
    return err
  }

  return nil
}
```
> &nbsp;&nbsp;&nbsp;&nbsp;在客户端发起调用时介绍过，它会阻塞在MessageExchange.recvCh，当响应回来时会根据stream Id（上面的frame.Header.ID） 找到对应的MessageExchange，并把frame放入recvCh，完成响应。这一步就体现在上面的代码。


## 结语

>  &nbsp;&nbsp;&nbsp;&nbsp;至此UBER的RPC框架TChannel————多路复用介绍完，感谢UBER团队的贡献，让我收益很多。
