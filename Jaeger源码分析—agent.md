## agent udp使用

#### 初始化udp

> 在agent开启时初始化了3个UDP服务

> 每个服务对应处理不同的数据格式

> 官方推荐使用6831端口接收数据

> github.com/uber/jaeger/cmd/agent/app/flags.go #35

```golang
var defaultProcessors = []struct {
	model    model
	protocol protocol
	hostPort string
}{
	{model: "zipkin", protocol: "compact", hostPort: ":5775"},
	{model: "jaeger", protocol: "compact", hostPort: ":6831"},
	{model: "jaeger", protocol: "binary", hostPort: ":6832"},
}
```

#### UDP服务端初始化

> github.com/uber/jaeger/cmd/agent/app/servers/thriftudp/transport.go #73

```golang
func NewTUDPServerTransport(hostPort string) (*TUDPTransport, error) {
    addr, err := net.ResolveUDPAddr("udp", hostPort)
    if err != nil {
    	return nil, thrift.NewTTransportException(thrift.NOT_OPEN, err.Error())
    }
    conn, err := net.ListenUDP(addr.Network(), addr)
    if err != nil {
    	return nil, thrift.NewTTransportException(thrift.NOT_OPEN, err.Error())
    }
    return &TUDPTransport{addr: conn.LocalAddr(), conn: conn}, nil
}
```

#### 初始化worker
> github.com/uber/jaeger/cmd/agent/app/processors/thrift_processor.go #78

```golang
// Serve initiates the readers and starts serving traffic
func (s *ThriftProcessor) Serve() {
	s.processing.Add(s.numProcessors)
	for i := 0; i < s.numProcessors; i++ {
		go s.processBuffer()
	}

	s.server.Serve()
}
```

#### 数据接收使用对象池和队列处理

> github.com/uber/jaeger/cmd/agent/app/servers/tbuffered_server.go

```golang
func NewTBufferedServer(
	transport thrift.TTransport,
	maxQueueSize int,
	maxPacketSize int,
	mFactory metrics.Factory,
) (*TBufferedServer, error) {
    //数据队列
    dataChan := make(chan *ReadBuf, maxQueueSize)
    //对象池初始化
    var readBufPool = &sync.Pool{
    	New: func() interface{} {
    		return &ReadBuf{bytes: make([]byte, maxPacketSize)}
    	},
    }
    
    res := &TBufferedServer{dataChan: dataChan,
    	transport:     transport,
    	maxQueueSize:  maxQueueSize,
    	maxPacketSize: maxPacketSize,
    	readBufPool:   readBufPool,
    }
    metrics.Init(&res.metrics, mFactory, nil)
    return res, nil
}
```

#### 从客户端接收数据放入队列

> github.com/uber/jaeger/cmd/agent/app/servers/tbuffered_server.go #80

```
func (s *TBufferedServer) Serve() {
	atomic.StoreUint32(&s.serving, 1)
	for s.IsServing() {
		readBuf := s.readBufPool.Get().(*ReadBuf)
		n, err := s.transport.Read(readBuf.bytes)
		if err == nil {
			readBuf.n = n
			s.metrics.PacketSize.Update(int64(n))
			select {
			case s.dataChan <- readBuf:
				s.metrics.PacketsProcessed.Inc(1)
				s.updateQueueSize(1)
			default:
			   //这里需要注意，如果写比处理快，agent将会扔掉超出的部分数据
			   s.metrics.PacketsDropped.Inc(1)
			}
		} else {
			s.metrics.ReadError.Inc(1)
		}
	}
}
```

>  注意点：agent的队列默认存放1000条消息，超过部分会被扔掉。

> 基于服务器的承载能力可以适当调节参数，减少数据的丢失，保持调用链的完整。
- 增加队列长度（default 1000） --processor.zipkin-compact.server-queue-size
- 增加处理协成数 （default 50） --processor.zipkin-compact.workers 
- 增加agent服务节点


#### 处理队列数据

> github.com/uber/jaeger/cmd/agent/app/processors/thrift_processor.go #104

```golang
func (s *ThriftProcessor) processBuffer() {
	for readBuf := range s.server.DataChan() {
		protocol := s.protocolPool.Get().(thrift.TProtocol)
		protocol.Transport().Write(readBuf.GetBytes())
		s.server.DataRecd(readBuf) // acknowledge receipt and release the buffer

		if ok, _ := s.handler.Process(protocol, protocol); !ok {
			// TODO log the error
			s.metrics.HandlerProcessError.Inc(1)
		}
		s.protocolPool.Put(protocol)
	}
	s.processing.Done()
}
```

## jaeger 解析6831端口的数据

> github.com/uber/jaeger/thrift-gen/jaeger/agent.go #105

```
func (p *AgentProcessor) Process(iprot, oprot thrift.TProtocol) (success bool, err thrift.TException) {
	name, _, seqId, err := iprot.ReadMessageBegin()

	if err != nil {
		return false, err
	}
	if processor, ok := p.GetProcessorFunction(name); ok {
		return processor.Process(seqId, iprot, oprot)
	}
	iprot.Skip(thrift.STRUCT)
	iprot.ReadMessageEnd()
	x7 := thrift.NewTApplicationException(thrift.UNKNOWN_METHOD, "Unknown function "+name)
	oprot.WriteMessageBegin(name, thrift.EXCEPTION, seqId)
	x7.Write(oprot)
	oprot.WriteMessageEnd()
	oprot.Flush()
	return false, x7

}
```

## 解析EmitBatch数据
>github.com/uber/jaeger/thrift-gen/agent/agent.go #187

```
func (p *agentProcessorEmitBatch) Process(seqId int32, iprot, oprot thrift.TProtocol) (success bool, err thrift.TException) {
	args := AgentEmitBatchArgs{}
	if err = args.Read(iprot); err != nil {
		iprot.ReadMessageEnd()
		return false, err
	}

	iprot.ReadMessageEnd()
	var err2 error
	if err2 = p.handler.EmitBatch(args.Batch); err2 != nil {
		return true, err2
	}
	return true, nil
}
```


## 数据解析完后使用tchan 提交数据
> github.com/uber/jaeger/thrift-gen/jaeger/tchan-jaeger.go #39

```
func (c *tchanCollectorClient) SubmitBatches(ctx thrift.Context, batches []*Batch) ([]*BatchSubmitResponse, error) {
	var resp CollectorSubmitBatchesResult
	args := CollectorSubmitBatchesArgs{
		Batches: batches,
	}
	success, err := c.client.Call(ctx, c.thriftService, "submitBatches", &args, &resp)
	if err == nil && !success {
	}

	return resp.GetSuccess(), err
}
```


